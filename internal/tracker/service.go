package tracker

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/steam"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
)

type Steam interface {
	Details(context.Context, int64, string) (domain.Price, error)
	Prices(context.Context, []int64, string) (map[int64]domain.Price, error)
	Catalog(context.Context, func(context.Context, []domain.App) error) error
}

type AddRequest struct {
	OwnerID   string
	RequestID string
	Game      string
	Country   string
	Condition string
	Budget    string
	Recurring bool
}

type Service struct {
	Store  *store.Store
	Steam  Steam
	Config *config.Config
	users  userLocks
}

func (s *Service) Price(ctx context.Context, input, country string) (domain.Price, error) {
	country, err := steam.NormalizeCountry(country)
	if err != nil {
		return domain.Price{}, err
	}

	id, err := steam.ParseAppID(input)
	if err != nil {
		return domain.Price{}, err
	}

	if id == 0 {
		name := strings.TrimSpace(input)
		exact, err := s.Store.ExactApp(ctx, name)
		if err == nil {
			id = exact.ID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.Price{}, err
		} else {
			apps, err := s.Store.Search(ctx, name)
			if err != nil {
				return domain.Price{}, err
			}

			if len(apps) == 0 {
				return domain.Price{}, &domain.PublicError{
					Code:    "NOT_FOUND",
					Message: "No catalog match was found. Try an exact Steam app ID or store URL.",
				}
			}

			if len(apps) != 1 {
				return domain.Price{}, domain.Invalid("Multiple Steam items matched. Retry with an exact name or app ID.")
			}

			id = apps[0].ID
		}
	}

	p, err := s.Steam.Details(ctx, id, country)
	if err != nil {
		return domain.Price{}, err
	}

	if p.Type != "game" && p.Type != "dlc" {
		return domain.Price{}, domain.Invalid("I only check and track Steam games and DLC.")
	}

	if p.Availability == "free" {
		return domain.Price{}, domain.Invalid("That item is free to play, so I have no paid Steam price to check or track.")
	}

	return p, nil
}

func (s *Service) Add(ctx context.Context, req AddRequest) (*models.Rule, domain.Price, error) {
	release, err := s.users.acquire(ctx, req.OwnerID)
	if err != nil {
		return nil, domain.Price{}, err
	}
	defer release()

	p, err := s.Price(ctx, req.Game, req.Country)
	if err != nil {
		return nil, p, err
	}

	var budget *domain.Money
	switch req.Condition {
	case domain.AnySale:
	case domain.UnderBudget:
		parsed, err := domain.ParseBudget(req.Budget)
		if err != nil {
			return nil, p, err
		}

		if p.Availability != "priced" || p.Currency != parsed.Currency {
			return nil, p, domain.Invalid("The budget currency must match a trustworthy current regional Steam price.")
		}

		budget = &parsed
	default:
		return nil, p, domain.Invalid("That tracking condition is not supported.")
	}

	r, err := s.Store.Add(ctx, store.AddRequest{
		OwnerID:   req.OwnerID,
		RequestID: req.RequestID,
		Price:     p,
		Condition: req.Condition,
		Budget:    budget,
		Recurring: req.Recurring,
		Maximum:   int(s.Config.MaximumActiveTracksPerUser),
		Interval:  s.Config.PollInterval,
	})

	return r, p, err
}

func (s *Service) Remove(ctx context.Context, owner, id string) error {
	release, err := s.users.acquire(ctx, owner)
	if err != nil {
		return err
	}
	defer release()

	return s.Store.Remove(ctx, owner, id)
}
