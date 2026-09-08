package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Delivery struct{ ent.Schema }

func (Delivery) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (Delivery) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_id").Unique(),
		field.String("destination_id").NotEmpty(),
		field.Enum("status").Values("pending", "sending", "retry", "sent", "dead", "cancelled").Default("pending"),
		field.Int("attempt_count").Default(0).NonNegative(),
		field.Time("next_attempt_at").Default(time.Now),
		field.String("message_id").Default(""),
		field.String("last_error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Delivery) Edges() []ent.Edge {
	return []ent.Edge{edge.From("event", Event.Type).Ref("deliveries").Field("event_id").Unique().Required()}
}

func (Delivery) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status", "next_attempt_at"), index.Fields("event_id").Unique()}
}

func (Delivery) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Checks(map[string]string{
		"delivery_status":   "status IN ('pending', 'sending', 'retry', 'sent', 'dead', 'cancelled')",
		"delivery_attempts": "attempt_count >= 0",
	})}
}
