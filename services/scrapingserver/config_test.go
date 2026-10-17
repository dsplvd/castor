package scrapingserver

import (
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
)

func TestDefaultsAreValidAndKeepBrowserSandbox(t *testing.T) {
	d := defaults()
	if err := validator.New().Struct(d); err != nil {
		t.Fatalf("default configuration cannot start: %v", err)
	}
	if d.Browser.NoSandbox {
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
		cfg.Scraping.Listen = tc.listen
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
		{"zero timeout", func(c *ServiceConfig) { c.Browser.Timeout = 0 }, false},
		{"negative timeout", func(c *ServiceConfig) { c.Browser.Timeout = -time.Second }, false},
		{"zero concurrency", func(c *ServiceConfig) { c.Capture.MaxConcurrency = 0 }, false},
		{"one concurrency", func(c *ServiceConfig) { c.Capture.MaxConcurrency = 1 }, true},
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
