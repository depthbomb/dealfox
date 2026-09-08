package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type FreeMonitor struct{ ent.Schema }

func (FreeMonitor) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.Time("checked_at").Optional().Nillable(),
		field.Time("next_check_at").Default(time.Now),
		field.String("status").Default("not checked"),
		field.Strings("problems").Optional(),
		field.Int("failures").Default(0),
	}
}
