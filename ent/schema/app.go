package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type App struct{ ent.Schema }

func (App) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Positive().Immutable().Annotations(&entsql.Annotation{
			Incremental: new(false),
		}),
		field.String("name").NotEmpty(),
		field.String("type"),
		field.Int64("last_modified").Default(0),
		field.Int64("price_change_number").Default(0),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (App) Edges() []ent.Edge {
	return []ent.Edge{edge.To("targets", Target.Type)}
}
