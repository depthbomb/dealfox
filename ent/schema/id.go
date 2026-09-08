package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/depthbomb/cuid2"
)

type IDMixin struct{ mixin.Schema }

func (IDMixin) Fields() []ent.Field {
	return []ent.Field{field.String("id").DefaultFunc(cuid.Generate).Immutable().MaxLen(24)}
}
