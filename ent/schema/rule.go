package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/depthbomb/dealfox/internal/domain"
)

type Rule struct{ ent.Schema }

func (Rule) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (Rule) Fields() []ent.Field {
	return []ent.Field{
		field.String("target_id"),
		field.String("owner_id").NotEmpty(),
		field.String("request_id").Unique().Optional().Nillable(),
		field.Enum("condition").Values(domain.AnySale, domain.UnderBudget),
		field.Int64("budget_minor").Optional().Nillable().NonNegative(),
		field.String("budget_currency").Default(""),
		field.Bool("recurring").Default(false),
		field.Bool("enabled").Default(true),
		field.Bool("latched").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Rule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("target", Target.Type).Ref("rules").Field("target_id").Unique().Required(),
		edge.To("events", Event.Type),
	}
}

func (Rule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_id", "enabled"),
		index.Fields("target_id", "enabled"),
		index.Fields("owner_id", "target_id", "condition").Unique().Annotations(entsql.IndexWhere("enabled")),
	}
}

func (Rule) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Checks(map[string]string{
		"rule_budget": "(condition = 'any_sale' AND budget_minor IS NULL AND budget_currency = '') OR (condition = 'sale_under_budget' AND budget_minor IS NOT NULL AND budget_minor >= 0 AND budget_currency ~ '^[A-Z]{3}$')",
	})}
}
