package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/depthbomb/dealfox/internal/freegames"
)

type FreeDelivery struct{ ent.Schema }

func (FreeDelivery) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (FreeDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.String("offer_id").NotEmpty(),
		field.String("subscription_id").NotEmpty(),
		field.Int("offer_generation").Default(1).Positive(),
		field.Enum("destination_kind").Values("dm", "channel"),
		field.String("destination_id").NotEmpty(),
		field.JSON("payload", freegames.Offer{}),
		field.Enum("status").Values("pending", "sending", "retry", "sent", "dead", "cancelled").Default("pending"),
		field.Int("attempt_count").Default(0).NonNegative(),
		field.Time("next_attempt_at").Default(time.Now),
		field.String("message_id").Default(""),
		field.String("last_error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (FreeDelivery) Indexes() []ent.Index {
	return []ent.Index{index.Fields("offer_id", "offer_generation", "subscription_id").Unique(), index.Fields("status", "next_attempt_at")}
}
