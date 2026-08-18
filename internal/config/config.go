// Package config loads and validates LeafMark's environment-variable
// configuration, failing fast with every problem collected into one error
// rather than stopping at the first.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds LeafMark's full runtime configuration.
type Config struct {
	GoodreadsUser        string
	GoodreadsPassword    string
	GoodreadsUserID      string
	KOReaderSyncURL      string
	KOReaderSyncUsername string
	KOReaderSyncPassword string
	NtfyURL              string
	LeafMarkBaseURL      string
	PollInterval         time.Duration
	MatchThreshold       float64
	DBPath               string
	ListenAddr           string
}

// Load reads configuration from the environment. If LEAFMARK_ENV_FILE is
// set, that dotenv-format file is loaded first via godotenv, filling any
// variables not already set in the process environment (so an operator can
// still override individual fields via a plain env var). This lets secrets
// be delivered as a mounted, permission-restricted file in production
// instead of showing up in `docker inspect`/`docker compose config`.
func Load() (*Config, error) {
	if path := os.Getenv("LEAFMARK_ENV_FILE"); path != "" {
		if err := godotenv.Load(path); err != nil {
			return nil, fmt.Errorf("config: load LEAFMARK_ENV_FILE %q: %w", path, err)
		}
	}

	var c collector
	cfg := &Config{
		GoodreadsUser:        c.require("GOODREADS_USER"),
		GoodreadsPassword:    c.require("GOODREADS_PASSWORD"),
		GoodreadsUserID:      c.require("GOODREADS_USER_ID"),
		KOReaderSyncURL:      c.requireURL("KOREADER_SYNC_URL"),
		KOReaderSyncUsername: c.require("KOREADER_SYNC_USERNAME"),
		KOReaderSyncPassword: c.require("KOREADER_SYNC_PASSWORD"),
		NtfyURL:              c.requireURL("NTFY_URL"),
		LeafMarkBaseURL:      c.requireHTTPSURL("LEAFMARK_BASE_URL"),
		DBPath:               c.require("DB_PATH"),
		ListenAddr:           c.optionalString("LISTEN_ADDR", ":8080"),
		PollInterval:         c.optionalDuration("POLL_INTERVAL", 5*time.Minute),
		MatchThreshold:       c.optionalUnitFloat("MATCH_THRESHOLD", 0.8),
	}

	if err := c.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// collector accumulates every validation problem so Load reports them all
// at once instead of forcing the user through one fix-and-restart cycle per
// missing var.
type collector struct {
	errs []error
}

func (c *collector) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return errors.Join(c.errs...)
}

func (c *collector) require(key string) string {
	v := os.Getenv(key)
	if v == "" {
		c.errs = append(c.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (c *collector) requireURL(key string) string {
	v := c.require(key)
	if v == "" {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		c.errs = append(c.errs, fmt.Errorf("%s must be a valid absolute URL, got %q", key, v))
	}
	return v
}

func (c *collector) requireHTTPSURL(key string) string {
	v := c.requireURL(key)
	if v != "" && !strings.HasPrefix(v, "https://") {
		// Not just style: ntfy's HTTP notification actions require HTTPS to
		// fire on iOS, and this is also the URL LeafMark's own confirm
		// endpoint/WebUI links are built from.
		c.errs = append(c.errs, fmt.Errorf("%s must be an https:// URL, got %q", key, v))
	}
	return v
}

func (c *collector) optionalString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *collector) optionalDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s must be a valid duration (e.g. \"5m\"), got %q: %w", key, v, err))
		return def
	}
	return d
}

func (c *collector) optionalUnitFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s must be a number between 0.0 and 1.0, got %q: %w", key, v, err))
		return def
	}
	if f < 0 || f > 1 {
		c.errs = append(c.errs, fmt.Errorf("%s must be between 0.0 and 1.0, got %v", key, f))
	}
	return f
}
