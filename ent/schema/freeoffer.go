package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/depthbomb/dealfox/internal/freegames"
)

type FreeOffer struct{ ent.Schema }

func (FreeOffer) Mixin() []ent.Mixin {
	return []ent.Mixin{IDMixin{}}
}

func (FreeOffer) Fields() []ent.Field {
	return []ent.Field{
		field.String("identity").Unique(),
		field.String("source").NotEmpty(),
		field.JSON("payload", freegames.Offer{}),
		field.Bool("eligible").Default(false),
		field.Bool("closed").Default(false),
		field.Int("generation").Default(1).Positive(),
		field.Time("first_seen_at").Default(time.Now).Immutable(),
		field.Time("last_seen_at").Default(time.Now),
	}
}

func (FreeOffer) Indexes() []ent.Index {
	return []ent.Index{index.Fields("source", "eligible")}
}
