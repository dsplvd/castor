package apiserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/internal/settings"
	"github.com/urfave/cli/v3"
)

func TestAPIServiceSettingsLoadFromNestedSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("servers:\n  api:\n    network:\n      timeout: 3s\n    devices:\n      roku:\n        app_id: channel-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func() *Config {
		t.Helper()
		var got *Config
		command := &cli.Command{
			Name: "castor", Flags: []cli.Flag{settings.Flag},
			Action: func(_ context.Context, cmd *cli.Command) (err error) {
				got, err = settings.Load(cmd, defaults())
				return err
			},
		}
		if err := command.Run(context.Background(), []string{"castor", "--config", path}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := load(); got.Servers.API.Network.Timeout != 3*time.Second || got.Servers.API.Devices.Roku.AppID != "channel-123" {
		t.Errorf("nested API settings = %+v", got.Servers.API)
	}
	t.Setenv("CASTOR_SERVERS__API__NETWORK__TIMEOUT", "4s")
	if got := load().Servers.API.Network.Timeout; got != 4*time.Second {
		t.Errorf("API timeout from environment = %s, want 4s", got)
	}
}
