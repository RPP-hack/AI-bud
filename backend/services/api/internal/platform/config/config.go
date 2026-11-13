package config

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"ai-bud/services/api/internal/access/application"
)

type Config struct {
	DatabaseURL, HTTPAddr, MockAddr, MockURL, MockToken string
	GrantAfter                                          time.Duration
	ReconcileEnabled                                    bool
	Reconciliation                                      application.ReconciliationOptions
}

func Load(get func(string) string) (Config, error) {
	if get("DEMO_MODE") != "true" {
		return Config{}, fmt.Errorf("this build requires DEMO_MODE=true: corporate authentication is not connected")
	}
	c := Config{DatabaseURL: get("DATABASE_URL"), HTTPAddr: get("HTTP_ADDR"), MockAddr: get("MOCK_ADDR"), MockURL: get("MOCK_URL"), MockToken: get("MOCK_TOKEN")}
	if c.DatabaseURL == "" || c.MockToken == "" {
		return Config{}, fmt.Errorf("DATABASE_URL and MOCK_TOKEN are required")
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = "127.0.0.1:8080"
	}
	if c.MockAddr == "" {
		c.MockAddr = "127.0.0.1:8090"
	}
	if c.MockURL == "" {
		c.MockURL = "http://127.0.0.1:8090"
	}
	u, err := url.Parse(c.MockURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("MOCK_URL must be an HTTP(S) URL")
	}
	d := get("MOCK_GRANT_AFTER")
	if d == "" {
		d = "30s"
	}
	c.GrantAfter, err = time.ParseDuration(d)
	if err != nil || c.GrantAfter < 0 {
		return Config{}, fmt.Errorf("MOCK_GRANT_AFTER must be a non-negative duration")
	}
	c.ReconcileEnabled = true
	if enabled := get("RECONCILE_ENABLED"); enabled != "" {
		c.ReconcileEnabled, err = strconv.ParseBool(enabled)
		if err != nil {
			return Config{}, fmt.Errorf("RECONCILE_ENABLED must be a boolean")
		}
	}
	c.Reconciliation = application.ReconciliationOptions{
		Interval: time.Second, PollInterval: 5 * time.Second, Timeout: 10 * time.Second,
		LeaseDuration: 30 * time.Second, RetryBase: 5 * time.Second, RetryMax: 5 * time.Minute, BatchSize: 32,
	}
	for _, field := range []struct {
		name   string
		target *time.Duration
	}{
		{"RECONCILE_INTERVAL", &c.Reconciliation.Interval},
		{"RECONCILE_POLL_INTERVAL", &c.Reconciliation.PollInterval},
		{"RECONCILE_TIMEOUT", &c.Reconciliation.Timeout},
		{"RECONCILE_LEASE", &c.Reconciliation.LeaseDuration},
		{"RECONCILE_RETRY_BASE", &c.Reconciliation.RetryBase},
		{"RECONCILE_RETRY_MAX", &c.Reconciliation.RetryMax},
	} {
		if value := get(field.name); value != "" {
			*field.target, err = time.ParseDuration(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s must be a duration", field.name)
			}
		}
	}
	if value := get("RECONCILE_BATCH_SIZE"); value != "" {
		c.Reconciliation.BatchSize, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("RECONCILE_BATCH_SIZE must be an integer")
		}
	}
	if err := c.Reconciliation.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
