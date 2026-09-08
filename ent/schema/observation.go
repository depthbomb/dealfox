package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/depthbomb/dealfox/internal/domain"
)

type Observation struct{ ent.Schema }

func (Observation) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (Observation) Fields() []ent.Field {
	return []ent.Field{
		field.String("target_id"),
		field.Time("observed_at"),
		field.JSON("price", domain.Price{}),
	}
}

func (Observation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("target", Target.Type).Ref("observations").Field("target_id").Unique().Required(),
		edge.To("events", Event.Type),
	}
}

func (Observation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("target_id", "observed_at"), index.Fields("observed_at")}
}
