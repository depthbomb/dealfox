package app

import (
	"context"
	"errors"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/rest"
	"github.com/tomogo-framework/snowflake"
)

func applicationID(ctx context.Context, cfg *config.Config, client *rest.Client) (api.ID, error) {
	var applicationID api.ID
	var err error
	if cfg.ApplicationID != nil {
		applicationID, err = snowflake.Parse(*cfg.ApplicationID)
	} else {
		application, _, getErr := client.Applications().GetCurrent(ctx)
		applicationID = application.ID
		err = getErr
	}

	if err != nil {
		return 0, err
	}

	if applicationID == 0 {
		return 0, errors.New("application ID must be a positive Discord snowflake")
	}

	return applicationID, nil
}

func commandCatalog(ctx context.Context, cfg *config.Config, client *rest.Client) (*commands.Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, err := applicationID(ctx, cfg, client)
	if err != nil {
		return nil, err
	}
	remote, _, err := client.Commands().ListGlobal(ctx, id, false)
	if err != nil {
		return nil, err
	}

	return commands.NewCatalog(remote)
}
