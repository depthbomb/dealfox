package store

import (
	"context"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/app"
	"github.com/depthbomb/dealfox/internal/domain"
)

func (s *Store) UpsertCatalog(ctx context.Context, apps []domain.App) error {
	for start := 0; start < len(apps); start += 1000 {
		end := min(start+1000, len(apps))
		builders := make([]*ent.AppCreate, 0, end-start)
		for _, a := range apps[start:end] {
			builders = append(builders, s.Client.App.Create().SetID(a.ID).SetName(a.Name).SetType(a.Type).SetLastModified(a.LastModified).SetPriceChangeNumber(a.PriceChangeNumber))
		}

		if err := s.Client.App.CreateBulk(builders...).OnConflictColumns(app.FieldID).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) Search(ctx context.Context, query string) ([]*ent.App, error) {
	return s.Client.App.Query().Where(app.TypeIn("game", "dlc"), app.NameContainsFold(query)).Order(ent.Asc(app.FieldName)).Limit(10).All(ctx)
}

func (s *Store) ExactApp(ctx context.Context, name string) (*ent.App, error) {
	return s.Client.App.Query().Where(app.TypeIn("game", "dlc"), app.NameEqualFold(name)).Order(ent.Asc(app.FieldID)).First(ctx)
}
