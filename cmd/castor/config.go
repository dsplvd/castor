package main

import (
	"errors"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/cast"
	"github.com/stupside/castor/cmd/castor/internal/sources"
	"github.com/stupside/castor/internal/settings"
)

// Config is the command line's sections of castor's configuration.
type Config struct {
	Device  cast.Device   `yaml:"device" settings:"strict"`
	Sources sources.Sites `yaml:"sources" settings:"strict" validate:"dive"`
	TMDB    tmdb.Config   `yaml:"tmdb" settings:"strict"`
	Servers struct {
		API settings.Endpoint `yaml:"api"`
	} `yaml:"servers"`
}

// browsable refuses an interactive cast that has no catalog to browse.
func (c Config) browsable() error {
	if c.TMDB.APIKey == "" {
		return errors.New("TMDB API key missing: set tmdb.api_key in config.yaml or CASTOR_TMDB__API_KEY env var")
	}
	return nil
}
