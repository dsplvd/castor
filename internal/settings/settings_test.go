package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sections stands for a command's own view of castor's configuration.
type sections struct {
	Cast struct {
		MaxHeight uint32 `yaml:"max_height"`
	} `yaml:"cast"`
	Servers struct {
		API struct {
			Network struct {
				Timeout time.Duration `yaml:"timeout"`
			} `yaml:"network"`
		} `yaml:"api"`
	} `yaml:"servers"`
}

func defaulted() sections {
	var s sections
	s.Servers.API.Network.Timeout = 5 * time.Second
	return s
}

// write lays config.yaml, and its overlay when given, in a fresh directory, and returns the file's path.
func write(t *testing.T, yaml, local string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if yaml != "" {
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.local.yaml"), []byte(local), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheOverlayThenTheEnvironmentWinOverTheFileAndTheDefaults(t *testing.T) {
	path := write(t, "cast:\n  max_height: 1080\nservers:\n  api:\n    network:\n      timeout: 2s\n", "cast:\n  max_height: 720\n")
	got, err := read(path, defaulted())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 720 || got.Servers.API.Network.Timeout != 2*time.Second {
		t.Errorf("read %+v, want the overlay's height over the file's, and the file's timeout over the default", got)
	}

	t.Setenv("CASTOR_CAST__MAX_HEIGHT", "480")
	if got, err = read(path, defaulted()); err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 480 {
		t.Errorf("max_height = %d, want the environment's 480", got.Cast.MaxHeight)
	}
}

func TestNoFileLeavesTheDefaults(t *testing.T) {
	got, err := read(filepath.Join(t.TempDir(), "config.yaml"), defaulted())
	if err != nil || got.Servers.API.Network.Timeout != 5*time.Second {
		t.Errorf("read %+v %v without a file, want the defaults", got, err)
	}
}

func TestAnUnreadableConfigPathIsNotTreatedAsAMissingFile(t *testing.T) {
	path := write(t, "network:\n  timeout: 2s\n", "")
	if _, err := read(filepath.Join(path, "config.yaml"), defaulted()); err == nil {
		t.Error("a path through a regular file was ignored instead of reporting the filesystem error")
	}
}

func TestUnknownKeysFailEvenInSectionsThisCommandDoesNotDecode(t *testing.T) {
	for _, tc := range []struct{ doc, unknown string }{
		{"api:\n  token: old-value\n", "api"},
		{"network:\n  timeout: 2s\n", "network"},
		{"resolver:\n  probe_max_concurrency: 2\n", "resolver"},
		{"devices:\n  roku:\n    app_id: example\n", "devices"},
		{"servers:\n  media:\n    tokne: typo\n", "servers.media.tokne"},
		{"sources:\n  - proxies: [https://example.com]\n    templates:\n      movvie: /watch\n", "sources[0].templates.movvie"},
	} {
		path := write(t, tc.doc, "")
		if _, err := read(path, defaulted()); err == nil || !strings.Contains(err.Error(), tc.unknown) {
			t.Errorf("config returned %v, want an unknown-key error naming %s", err, tc.unknown)
		}
	}
	t.Setenv("CASTOR_API__TOKEN", "old-value")
	if _, err := read(filepath.Join(t.TempDir(), "config.yaml"), defaulted()); err == nil || !strings.Contains(err.Error(), "api") {
		t.Errorf("old token environment variable returned %v, want an unknown-key error", err)
	}
}

func TestUnknownEnvironmentNamespacesStillFail(t *testing.T) {
	for _, tc := range []struct{ key, path string }{
		{"CASTOR_WEB__PASSWORD", "web"},
		{"CASTOR_TEST_CHROME", "test_chrome"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, "example")
			if _, err := read(filepath.Join(t.TempDir(), "config.yaml"), defaulted()); err == nil || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("%s returned %v, want unknown-key error naming %s", tc.key, err, tc.path)
			}
		})
	}
}

func TestKnownKeysInOtherCommandsSectionsRemainValid(t *testing.T) {
	path := write(t, "servers:\n  api:\n    token: secret\n  scraping:\n    browser:\n      timeout: 30s\nsources:\n  - proxies: [https://example.com]\n    templates:\n      movie: /watch\n      episode: /episode\n", "")
	if _, err := read(path, defaulted()); err != nil {
		t.Fatalf("known keys used by other commands were rejected: %v", err)
	}
}

func TestExampleConfigurationUsesKnownKeys(t *testing.T) {
	example, err := os.ReadFile("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := write(t, string(example), "")
	if _, err := read(path, defaulted()); err != nil {
		t.Fatalf("example configuration rejected: %v", err)
	}
}
