package mediaserver

import (
	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle/whisper"
)

// Config is the media server's sections of castor's configuration.
type Config struct {
	Resolver ResolverConfig `yaml:"resolver" validate:"required"`
	Server   ServerConfig   `yaml:"server" validate:"required"`
	Network  NetworkConfig  `yaml:"network"`
	Whisper  whisper.Config `yaml:"whisper"`
}

// ResolverConfig is how found streams are ranked.
type ResolverConfig struct {
	rank.Config `yaml:",inline"`
}

// ServerConfig is where `castor media-server` listens, where devices reach it, and the token its API asks for.
type ServerConfig struct {
	Listen string `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	// Advertise is where devices reach this server from outside its network.
	Advertise string `yaml:"advertise" validate:"omitempty,http_url"`
	// Token is what its API asks every request to carry; devices fetch what it serves without one.
	Token string `yaml:"token"`
}

// NetworkConfig is the interface devices reach this machine on.
type NetworkConfig struct {
	Interface string `yaml:"interface"`
}

func defaults() Config {
	return Config{
		Resolver: ResolverConfig{Config: rank.Config{ProbeMaxConcurrency: 2}},
		Server:   ServerConfig{Listen: ":8410"},
	}
}
