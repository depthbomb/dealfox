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

type Target struct{ ent.Schema }

func (Target) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (Target) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("app_id"),
		field.String("country").MinLen(2).MaxLen(2),
		field.Time("next_due_at").Default(time.Now),
		field.Time("last_observed_at").Optional().Nillable(),
		field.Int("failure_count").Default(0).NonNegative(),
		field.String("last_error").Default(""),
	}
}

func (Target) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).Ref("targets").Field("app_id").Unique().Required(),
		edge.To("rules", Rule.Type),
		edge.To("observations", Observation.Type),
	}
}

func (Target) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "country").Unique(), index.Fields("next_due_at")}
}

func (Target) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Checks(map[string]string{
		"target_country":  "country ~ '^[A-Z]{2}$'",
		"target_failures": "failure_count >= 0",
	})}
}
