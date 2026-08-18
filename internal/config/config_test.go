package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var validEnv = map[string]string{
	"GOODREADS_USER":         "reader@example.com",
	"GOODREADS_PASSWORD":     "hunter2",
	"GOODREADS_USER_ID":      "12345",
	"KOREADER_SYNC_URL":      "https://kosync.example.com",
	"KOREADER_SYNC_USERNAME": "koreader",
	"KOREADER_SYNC_PASSWORD": "koreaderpw",
	"NTFY_URL":               "https://ntfy.example.com/leafmark",
	"LEAFMARK_BASE_URL":      "https://leafmark.example.ts.net",
	"DB_PATH":                "/data/leafmark.db",
}

// setValidEnvExcept sets every required var to a valid value via t.Setenv
// (auto-reverted after the test), except the given keys, which are left
// untouched so a test can exercise "this var is genuinely absent."
func setValidEnvExcept(t *testing.T, skip ...string) {
	t.Helper()
	skipSet := make(map[string]bool, len(skip))
	for _, k := range skip {
		skipSet[k] = true
	}
	for k, v := range validEnv {
		if !skipSet[k] {
			t.Setenv(k, v)
		}
	}
}

func setValidEnv(t *testing.T) {
	t.Helper()
	setValidEnvExcept(t)
}

func TestLoadValidConfig(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GoodreadsUser != "reader@example.com" {
		t.Errorf("GoodreadsUser = %q", cfg.GoodreadsUser)
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("expected default PollInterval of 5m, got %v", cfg.PollInterval)
	}
	if cfg.MatchThreshold != 0.8 {
		t.Errorf("expected default MatchThreshold of 0.8, got %v", cfg.MatchThreshold)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected default ListenAddr of :8080, got %v", cfg.ListenAddr)
	}
}

func TestLoadMissingVarsAreAllReported(t *testing.T) {
	// Deliberately don't set anything.
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required vars")
	}
	for key := range validEnv {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("expected combined error to mention %s, got: %v", key, err)
		}
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	setValidEnv(t)
	t.Setenv("POLL_INTERVAL", "not-a-duration")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "POLL_INTERVAL") {
		t.Fatalf("expected POLL_INTERVAL error, got %v", err)
	}
}

func TestLoadMatchThresholdOutOfRange(t *testing.T) {
	setValidEnv(t)
	t.Setenv("MATCH_THRESHOLD", "1.5")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MATCH_THRESHOLD") {
		t.Fatalf("expected MATCH_THRESHOLD error, got %v", err)
	}
}

func TestLoadBaseURLMustBeHTTPS(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LEAFMARK_BASE_URL", "http://leafmark.example.ts.net")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LEAFMARK_BASE_URL") {
		t.Fatalf("expected LEAFMARK_BASE_URL scheme error, got %v", err)
	}
}

func TestLoadEnvFileFillsGapsWithoutOverridingEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "secrets.env")
	contents := "GOODREADS_USER=fromfile@example.com\nGOODREADS_PASSWORD=fromfilepw\n"
	if err := os.WriteFile(envFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	// GOODREADS_USER left genuinely unset so the file has to fill the gap;
	// GOODREADS_PASSWORD set explicitly so we can confirm env wins over file.
	setValidEnvExcept(t, "GOODREADS_USER")
	t.Setenv("GOODREADS_PASSWORD", "fromenv")
	t.Setenv("LEAFMARK_ENV_FILE", envFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GoodreadsUser != "fromfile@example.com" {
		t.Errorf("expected GoodreadsUser from file, got %q", cfg.GoodreadsUser)
	}
	if cfg.GoodreadsPassword != "fromenv" {
		t.Errorf("expected explicit env var to win over file, got %q", cfg.GoodreadsPassword)
	}
}
