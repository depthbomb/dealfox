package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/depthbomb/dealfox/internal/domain"
)

type Event struct{ ent.Schema }

func (Event) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (Event) Fields() []ent.Field {
	return []ent.Field{
		field.String("rule_id"),
		field.String("observation_id"),
		field.JSON("payload", domain.Payload{}),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (Event) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("rule", Rule.Type).Ref("events").Field("rule_id").Unique().Required(),
		edge.From("observation", Observation.Type).Ref("events").Field("observation_id").Unique().Required(),
		edge.To("deliveries", Delivery.Type),
	}
}

func (Event) Indexes() []ent.Index {
	return []ent.Index{index.Fields("rule_id", "observation_id").Unique()}
}
