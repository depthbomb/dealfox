package tracker

import (
	"context"
	"sync"
)

type userLock struct {
	gate chan struct{}
	refs int
}

type userLocks struct {
	mu      sync.Mutex
	entries map[string]*userLock
}

func (l *userLocks) acquire(ctx context.Context, user string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*userLock)
	}

	entry := l.entries[user]
	if entry == nil {
		entry = &userLock{
			gate: make(chan struct{}, 1),
		}

		l.entries[user] = entry
	}

	entry.refs++

	l.mu.Unlock()

	forget := func() {
		l.mu.Lock()
		defer l.mu.Unlock()

		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, user)
		}
	}

	select {
	case entry.gate <- struct{}{}:
		return func() {
			<-entry.gate
			forget()
		}, nil
	case <-ctx.Done():
		forget()

		return nil, ctx.Err()
	}
}
