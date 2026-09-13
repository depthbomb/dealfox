package schema

import (
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/nook"
	"github.com/depthbomb/nook/schema"
)

type Price = nook.JSON[domain.Price]
type Payload = nook.JSON[domain.Payload]
type Offer = nook.JSON[freegames.Offer]
type Problems = nook.JSON[[]string]

var Schema = schema.Database{
	Tables: []schema.Table{
		{
			Name:  "apps",
			Model: "App",
			Fields: []schema.Field{
				schema.Int64("id").PrimaryKey().Immutable().Check("app_id_positive", "id > 0"),
				schema.String("name").NotEmpty(),
				schema.String("type"),
				schema.Int64("last_modified").DefaultSQL("0"),
				schema.Int64("price_change_number").DefaultSQL("0"),
				// Store catalog writes supply update timestamps explicitly for bulk upserts.
				schema.Time("updated_at").DefaultFunc(time.Now),
				schema.String("name_fold").DefaultSQL("''"),
			},
			Relations: []schema.Relation{
				schema.HasMany("Targets", "targets", "app_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "apps_name_fold",
					Fields: []string{"name_fold"},
				},
			},
		},
		{
			Name:  "targets",
			Model: "Target",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.Int64("app_id").References("apps", "id"),
				schema.String("country").MaxLen(2),
				schema.Time("next_due_at").DefaultFunc(time.Now),
				schema.Time("last_observed_at").Nullable(),
				schema.Int64("failure_count").DefaultSQL("0").Check("target_failure_count_nonnegative", "failure_count >= 0"),
				schema.String("last_error").DefaultSQL("''"),
			},
			Relations: []schema.Relation{
				schema.BelongsTo("App", "apps", "app_id"),
				schema.HasMany("Rules", "rules", "target_id"),
				schema.HasMany("Observations", "observations", "target_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "targets_app_id_country",
					Fields: []string{"app_id", "country"},
					Unique: true,
					Where:  "",
				},
				{
					Name:   "targets_next_due_at",
					Fields: []string{"next_due_at"},
					Unique: false,
					Where:  "",
				},
			},
			Checks: []schema.Check{
				{
					Name:       "target_country",
					Expression: "length(country) = 2 AND country GLOB '[A-Z][A-Z]'",
				},
			},
		},
		{
			Name:  "rules",
			Model: "Rule",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("target_id").References("targets", "id"),
				schema.String("owner_id").NotEmpty(),
				schema.String("request_id").Unique().Nullable(),
				schema.Enum("condition").Values(domain.AnySale, domain.UnderBudget),
				schema.Int64("budget_minor").Nullable().Check("rule_budget_minor_nonnegative", "budget_minor >= 0"),
				schema.String("budget_currency").DefaultSQL("''"),
				schema.Bool("recurring").DefaultSQL("0"),
				schema.Bool("enabled").DefaultSQL("1"),
				schema.Bool("latched").DefaultSQL("0"),
				schema.Time("created_at").Immutable().DefaultFunc(time.Now),
				schema.Time("updated_at").DefaultFunc(time.Now).UpdateDefault(time.Now),
			},
			Relations: []schema.Relation{
				schema.BelongsTo("Target", "targets", "target_id"),
				schema.HasMany("Events", "events", "rule_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "rules_owner_id_enabled",
					Fields: []string{"owner_id", "enabled"},
					Unique: false,
					Where:  "",
				},
				{
					Name:   "rules_target_id_enabled",
					Fields: []string{"target_id", "enabled"},
					Unique: false,
					Where:  "",
				},
				{
					Name:   "rules_owner_id_target_id_condition",
					Fields: []string{"owner_id", "target_id", "condition"},
					Unique: true,
					Where:  "enabled = 1",
				},
			},
			Checks: []schema.Check{
				{
					Name:       "rule_budget",
					Expression: "(condition = 'any_sale' AND budget_minor IS NULL AND budget_currency = '') OR (condition = 'sale_under_budget' AND budget_minor IS NOT NULL AND budget_minor >= 0 AND length(budget_currency) = 3 AND budget_currency GLOB '[A-Z][A-Z][A-Z]')",
				},
			},
		},
		{
			Name:  "observations",
			Model: "Observation",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("target_id").References("targets", "id"),
				schema.Time("observed_at"),
				schema.String("price").GoType("github.com/depthbomb/dealfox/internal/store/schema", "Price"),
			},
			Relations: []schema.Relation{
				schema.BelongsTo("Target", "targets", "target_id"),
				schema.HasMany("Events", "events", "observation_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "observations_target_id_observed_at",
					Fields: []string{"target_id", "observed_at"},
					Unique: false,
					Where:  "",
				},
				{
					Name:   "observations_observed_at",
					Fields: []string{"observed_at"},
					Unique: false,
					Where:  "",
				},
			},
		},
		{
			Name:  "events",
			Model: "Event",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("rule_id").References("rules", "id"),
				schema.String("observation_id").References("observations", "id"),
				schema.String("payload").GoType("github.com/depthbomb/dealfox/internal/store/schema", "Payload"),
				schema.Time("created_at").Immutable().DefaultFunc(time.Now),
			},
			Relations: []schema.Relation{
				schema.BelongsTo("Rule", "rules", "rule_id"),
				schema.BelongsTo("Observation", "observations", "observation_id"),
				schema.HasMany("Deliveries", "deliveries", "event_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "events_rule_id_observation_id",
					Fields: []string{"rule_id", "observation_id"},
					Unique: true,
					Where:  "",
				},
			},
		},
		{
			Name:  "deliveries",
			Model: "Delivery",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("event_id").Unique().References("events", "id"),
				schema.String("destination_id").NotEmpty(),
				schema.Enum("status").Values("pending", "sending", "retry", "sent", "dead", "cancelled").DefaultSQL("'pending'"),
				schema.Int64("attempt_count").DefaultSQL("0").Check("delivery_attempt_count_nonnegative", "attempt_count >= 0"),
				schema.Time("next_attempt_at").DefaultFunc(time.Now),
				schema.String("message_id").DefaultSQL("''"),
				schema.String("last_error").DefaultSQL("''"),
				schema.Time("created_at").Immutable().DefaultFunc(time.Now),
				schema.Time("updated_at").DefaultFunc(time.Now).UpdateDefault(time.Now),
			},
			Relations: []schema.Relation{
				schema.BelongsTo("Event", "events", "event_id"),
			},
			Indexes: []schema.Index{
				{
					Name:   "deliveries_status_next_attempt_at",
					Fields: []string{"status", "next_attempt_at"},
					Unique: false,
					Where:  "",
				},
			},
		},
		{
			Name:  "free_offers",
			Model: "FreeOffer",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("identity").Unique(),
				schema.String("source").NotEmpty(),
				schema.String("payload").GoType("github.com/depthbomb/dealfox/internal/store/schema", "Offer"),
				schema.Bool("eligible").DefaultSQL("0"),
				schema.Bool("closed").DefaultSQL("0"),
				schema.Int64("generation").DefaultSQL("1").Check("freeoffer_generation_positive", "generation > 0"),
				schema.Time("first_seen_at").Immutable().DefaultFunc(time.Now),
				schema.Time("last_seen_at").DefaultFunc(time.Now),
			},
			Indexes: []schema.Index{
				{
					Name:   "free_offers_source_eligible",
					Fields: []string{"source", "eligible"},
					Unique: false,
					Where:  "",
				},
			},
		},
		{
			Name:  "free_deliveries",
			Model: "FreeDelivery",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("offer_id").NotEmpty(),
				schema.String("subscription_id").NotEmpty(),
				schema.Int64("offer_generation").DefaultSQL("1").Check("freedelivery_offer_generation_positive", "offer_generation > 0"),
				schema.Enum("destination_kind").Values("dm", "channel"),
				schema.String("destination_id").NotEmpty(),
				schema.String("payload").GoType("github.com/depthbomb/dealfox/internal/store/schema", "Offer"),
				schema.Enum("status").Values("pending", "sending", "retry", "sent", "dead", "cancelled").DefaultSQL("'pending'"),
				schema.Int64("attempt_count").DefaultSQL("0").Check("freedelivery_attempt_count_nonnegative", "attempt_count >= 0"),
				schema.Time("next_attempt_at").DefaultFunc(time.Now),
				schema.String("message_id").DefaultSQL("''"),
				schema.String("last_error").DefaultSQL("''"),
				schema.Time("created_at").Immutable().DefaultFunc(time.Now),
				schema.Time("updated_at").DefaultFunc(time.Now).UpdateDefault(time.Now),
			},
			Indexes: []schema.Index{
				{
					Name:   "free_deliveries_offer_id_offer_generation_subscription_id",
					Fields: []string{"offer_id", "offer_generation", "subscription_id"},
					Unique: true,
					Where:  "",
				},
				{
					Name:   "free_deliveries_status_next_attempt_at",
					Fields: []string{"status", "next_attempt_at"},
					Unique: false,
					Where:  "",
				},
			},
		},
		{
			Name:  "free_monitors",
			Model: "FreeMonitor",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey(),
				schema.Time("checked_at").Nullable(),
				schema.Time("next_check_at").DefaultFunc(time.Now),
				schema.String("status").DefaultSQL("'not checked'"),
				schema.String("problems").GoType("github.com/depthbomb/dealfox/internal/store/schema", "Problems").DefaultSQL("'[]'"),
				schema.Int64("failures").DefaultSQL("0"),
			},
		},
		{
			Name:  "free_subscriptions",
			Model: "FreeSubscription",
			Fields: []schema.Field{
				schema.String("id").PrimaryKey().DefaultFunc(cuid.Generate).Immutable().NotEmpty().MaxLen(24),
				schema.String("scope").NotEmpty(),
				schema.String("source").NotEmpty(),
				schema.Enum("destination_kind").Values("dm", "channel"),
				schema.String("destination_id").NotEmpty(),
				schema.String("managed_by").NotEmpty(),
				schema.Bool("enabled").DefaultSQL("1"),
				schema.Time("created_at").Immutable().DefaultFunc(time.Now),
				schema.Time("updated_at").DefaultFunc(time.Now).UpdateDefault(time.Now),
			},
			Indexes: []schema.Index{
				{
					Name:   "free_subscriptions_scope_source",
					Fields: []string{"scope", "source"},
					Unique: true,
					Where:  "",
				},
				{
					Name:   "free_subscriptions_source_enabled",
					Fields: []string{"source", "enabled"},
					Unique: false,
					Where:  "",
				},
			},
		},
	},
}
