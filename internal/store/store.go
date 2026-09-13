package store

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
	"github.com/gofrs/flock"
	"golang.org/x/sync/semaphore"
)

type Store struct {
	Client       *models.Client
	db           *nook.DB
	path         string
	deliveryGate *semaphore.Weighted
}

func discard[T any](_ T, err error) error {
	return err
}

// write acquires SQLite's writer before reading state used by a mutation.
func (s *Store) write(ctx context.Context, fn func(*models.Client) error) error {
	return s.Client.WithTx(ctx, fn)
}

func (s *Store) Close() error {
	return s.db.Close()
}

// LockProcess holds an OS lock for the bot's lifetime. Keep the file in place
// so all processes continue to lock the same inode after a clean shutdown.
func (s *Store) LockProcess(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := flock.New(s.path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()

		return nil, err
	}

	if !locked {
		_ = lock.Close()

		return nil, errors.New("another DealFox process holds the database lock")
	}

	return func() {
		_ = lock.Close()
	}, nil
}

func Open(ctx context.Context, path string) (*Store, error) {
	resolved, err := database.Path(path)
	if err != nil {
		return nil, err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return nil, errors.New("database file is unavailable; run dealfox database migrate")
	}
	db, err := nook.OpenWithOptions(ctx, resolved, database.Options())
	if err != nil {
		return nil, err
	}

	return &Store{
		Client:       models.New(db),
		db:           db,
		path:         resolved,
		deliveryGate: semaphore.NewWeighted(deliverySlots),
	}, nil
}
