package store

import (
	"context"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
)

func (s *Store) UpsertCatalog(ctx context.Context, apps []domain.App) error {
	for start := 0; start < len(apps); start += 1000 {
		end := min(start+1000, len(apps))
		inputs := make([]models.AppInput, 0, end-start)
		now := time.Now()
		for _, a := range apps[start:end] {
			inputs = append(inputs, models.AppInput{
				ID:                nook.Set(a.ID),
				Name:              nook.Set(a.Name),
				NameFold:          nook.Set(strings.ToLower(a.Name)),
				Type:              nook.Set(a.Type),
				LastModified:      nook.Set(a.LastModified),
				PriceChangeNumber: nook.Set(a.PriceChangeNumber),
				UpdatedAt:         nook.Set(now),
			})
		}

		if _, err := s.Client.App.CreateBulk(inputs...).OnConflict(models.AppColumns.ID).UpdateExcluded(models.AppColumns.Name, models.AppColumns.NameFold, models.AppColumns.Type, models.AppColumns.LastModified, models.AppColumns.PriceChangeNumber, models.AppColumns.UpdatedAt).Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) Search(ctx context.Context, query string) ([]*models.App, error) {
	return s.Client.App.Query().Where(models.AppColumns.Type.In("game", "dlc"), models.AppColumns.NameFold.Contains(strings.ToLower(query))).OrderBy(models.AppColumns.Name.Asc(), models.AppColumns.ID.Asc()).Limit(10).All(ctx)
}

func (s *Store) ExactApp(ctx context.Context, name string) (*models.App, error) {
	return s.Client.App.Query().Where(models.AppColumns.Type.In("game", "dlc"), models.AppColumns.NameFold.Eq(strings.ToLower(name))).OrderBy(models.AppColumns.ID.Asc()).First(ctx)
}
