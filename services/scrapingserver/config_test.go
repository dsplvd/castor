package scrapingserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/settings"
)

func TestScrapingOwnsBrowserAndCaptureSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "servers:\n  scraping:\n    listen: localhost:8412\n    browser:\n      chrome_path: /usr/bin/chromium\n      timeout: 45s\n    capture:\n      max_concurrency: 2\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CASTOR_SERVERS__SCRAPING__BROWSER__TIMEOUT", "20s")
	t.Setenv("CASTOR_SERVERS__SCRAPING__BROWSER__NO_SANDBOX", "true")
	command := &cli.Command{
		Name: "castor", Flags: []cli.Flag{settings.Flag},
		Action: func(_ context.Context, cmd *cli.Command) error {
			got, err := settings.Load(cmd, defaults())
			if err != nil {
				return err
			}
			if got.Servers.Scraping.Listen != "localhost:8412" || got.Servers.Scraping.Browser.ChromePath != "/usr/bin/chromium" || got.Servers.Scraping.Browser.Timeout != 20*time.Second || !got.Servers.Scraping.Browser.NoSandbox || got.Servers.Scraping.Capture.MaxConcurrency != 2 {
				t.Errorf("scraping config = %+v, want nested file settings with the environment's timeout", got.Servers.Scraping)
			}
			return nil
		},
	}
	if err := command.Run(context.Background(), []string{"castor", "--config", path}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsAreValidAndKeepBrowserSandbox(t *testing.T) {
	d := defaults()
	if err := validator.New().Struct(d); err != nil {
		t.Fatalf("default configuration cannot start: %v", err)
	}
	if d.Servers.Scraping.Browser.NoSandbox {
		t.Fatal("browser sandbox disabled by default")
	}
}

func TestListenRequiresAHostAndPort(t *testing.T) {
	for _, tc := range []struct {
		listen string
		ok     bool
	}{
		{":8412", true}, {"localhost:80", true}, {"example.com:8412", true},
		{"", false}, {"8412", false}, {"localhost", false},
	} {
		cfg := defaults()
		cfg.Servers.Scraping.Listen = tc.listen
		if err := validator.New().Struct(cfg); (err == nil) != tc.ok {
			t.Errorf("listen %q: err = %v, want ok=%v", tc.listen, err, tc.ok)
		}
	}
}

func TestBrowserRequiresPositiveTimeoutAndConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ServiceConfig)
		ok   bool
	}{
		{"zero timeout", func(c *ServiceConfig) { c.Servers.Scraping.Browser.Timeout = 0 }, false},
		{"negative timeout", func(c *ServiceConfig) { c.Servers.Scraping.Browser.Timeout = -time.Second }, false},
		{"zero concurrency", func(c *ServiceConfig) { c.Servers.Scraping.Capture.MaxConcurrency = 0 }, false},
		{"one concurrency", func(c *ServiceConfig) { c.Servers.Scraping.Capture.MaxConcurrency = 1 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			tc.edit(&cfg)
			if err := validator.New().Struct(cfg); (err == nil) != tc.ok {
				t.Errorf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
