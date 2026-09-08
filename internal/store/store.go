package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/depthbomb/dealfox/ent"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/sync/semaphore"
)

type Store struct {
	Client       *ent.Client
	db           *sql.DB
	mu           sync.Mutex
	deliveryGate *semaphore.Weighted
}

func (s *Store) write(ctx context.Context, fn func(*ent.Client) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx.Client()); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) Close() error {
	return s.Client.Close()
}

// LockProcess holds a session lock for the lifetime of the single bot process.
func (s *Store) LockProcess(ctx context.Context) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(715725563902)").Scan(&acquired); err != nil || !acquired {
		conn.Close()

		return nil, errors.New("another DealFox process holds the database lock")
	}

	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(715725563902)")
		_ = conn.Close()
	}, nil
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, errors.New("could not connect to PostgreSQL")
	}

	return &Store{
		Client:       ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db))),
		db:           db,
		deliveryGate: semaphore.NewWeighted(deliverySlots),
	}, nil
}
