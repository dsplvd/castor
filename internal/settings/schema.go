package settings

import "fmt"

// keySchema covers the union of sections read by Castor's commands. A command
// still decodes only its own fields, but typos anywhere in the shared document fail.
type keySchema map[string]any

var configKeys = keySchema{
	"device": keySchema{"name": nil, "type": nil, "host": nil, "id": nil},
	"sources": []any{keySchema{
		"proxies":   []any{nil},
		"templates": keySchema{"movie": nil, "episode": nil},
	}},
	"tmdb": keySchema{"api_key": nil},
	"cast": keySchema{"delivery": nil, "max_height": nil, "subtitles": nil, "audio": nil},
	"servers": keySchema{
		"api": keySchema{
			"url": nil, "listen": nil, "token": nil,
			"network": keySchema{"timeout": nil},
			"devices": keySchema{"roku": keySchema{"app_id": nil, "password": nil}},
		},
		"media": keySchema{
			"url": nil, "listen": nil, "token": nil, "advertise": nil,
			"whisper":  keySchema{"model_path": nil},
			"resolver": keySchema{"probe_max_concurrency": nil},
			"network":  keySchema{"interface": nil},
		},
		"scraping": keySchema{
			"url": nil, "listen": nil, "token": nil,
			"browser": keySchema{"chrome_path": nil, "headless": nil, "no_sandbox": nil, "timeout": nil},
			"capture": keySchema{"max_concurrency": nil},
		},
	},
	"telemetry": keySchema{"endpoint": nil},
}

func checkKeys(value, allowed any, path string) error {
	if value == nil {
		return nil
	}
	switch keys := allowed.(type) {
	case keySchema:
		fields, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("configuration section %q must be a mapping", path)
		}
		for key, child := range fields {
			name := key
			if path != "" {
				name = path + "." + key
			}
			next, known := keys[key]
			if !known {
				return fmt.Errorf("unknown configuration key %q", name)
			}
			if err := checkKeys(child, next, name); err != nil {
				return err
			}
		}
	case []any:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("configuration section %q must be a list", path)
		}
		for i, item := range items {
			if err := checkKeys(item, keys[0], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
