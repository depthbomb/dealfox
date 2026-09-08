package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type FreeSubscription struct{ ent.Schema }

func (FreeSubscription) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (FreeSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.String("scope").NotEmpty(),
		field.String("source").NotEmpty(),
		field.Enum("destination_kind").Values("dm", "channel"),
		field.String("destination_id").NotEmpty(),
		field.String("managed_by").NotEmpty(),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (FreeSubscription) Indexes() []ent.Index {
	return []ent.Index{index.Fields("scope", "source").Unique(), index.Fields("source", "enabled")}
}
