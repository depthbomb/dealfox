package tracker

import "context"

// LockAccount serializes personal changes with account deletion.
func (s *Service) LockAccount(ctx context.Context, owner string) (func(), error) {
	return s.users.acquire(ctx, owner)
}

func (s *Service) DeleteAccount(ctx context.Context, owner string) error {
	release, err := s.LockAccount(ctx, owner)
	if err != nil {
		return err
	}
	defer release()

	return s.Store.DeleteAccount(ctx, owner)
}
