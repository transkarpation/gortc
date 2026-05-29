// Package config loads and validates the server configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// App is a single API consumer. Its public APIKey identifies the AppID and
// selects the APISecret used to sign and verify that app's JWTs.
type App struct {
	AppID     string `json:"appId"`
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
}

// Config is the top-level application configuration.
type Config struct {
	Apps []App `json:"apps"`
}

// Load reads and validates JSON configuration from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if len(c.Apps) == 0 {
		return fmt.Errorf("no apps configured")
	}
	seen := make(map[string]bool, len(c.Apps))
	for i, a := range c.Apps {
		if a.AppID == "" || a.APIKey == "" || a.APISecret == "" {
			return fmt.Errorf("apps[%d] requires appId, apiKey and apiSecret", i)
		}
		if seen[a.APIKey] {
			return fmt.Errorf("duplicate apiKey %q", a.APIKey)
		}
		seen[a.APIKey] = true
	}
	return nil
}
