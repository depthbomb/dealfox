package app

import (
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/envschema"
)

func loadConfig(directory string) (config.Config, error) {
	if directory == "" {
		return config.Load()
	}

	source, err := envschema.EnvFileSource(directory, envschema.ProcessSource())
	if err != nil {
		return config.Config{}, err
	}

	return config.LoadSource(source)
}
