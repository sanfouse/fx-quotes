package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	resetEnv(t)
	const databaseURL = "postgres://localhost/fx_quotes"
	tests := []struct {
		name, databaseURL, ratesURL, wantRatesURL string
	}{
		{name: "default provider", databaseURL: databaseURL, wantRatesURL: "https://api.frankfurter.dev"},
		{name: "custom provider", databaseURL: databaseURL, ratesURL: "http://localhost:8080/api/", wantRatesURL: "http://localhost:8080/api/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.databaseURL)
			t.Setenv("RATES_BASE_URL", tt.ratesURL)
			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tt.databaseURL, cfg.DatabaseURL)
			assert.Equal(t, tt.wantRatesURL, cfg.Rates.BaseURL)
		})
	}
}

func TestLoadMissingDatabaseURL(t *testing.T) {
	resetEnv(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	assert.Error(t, err)
}

func TestExampleConfig(t *testing.T) {
	resetEnv(t)
	data, err := os.ReadFile("../../.env.example")
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			t.Setenv(key, strings.Trim(value, "'"))
		}
	}
	got, err := Load()
	require.NoError(t, err)
	want := Defaults()
	want.DatabaseURL = os.Getenv("DATABASE_URL")
	assert.Equal(t, want, got)
}

func TestSettingsOverrides(t *testing.T) {
	resetEnv(t)
	for key, value := range map[string]string{
		"DATABASE_PING_TIMEOUT":          "2s",
		"HTTP_PORT":                      "4000",
		"HTTP_READ_TIMEOUT":              "7s",
		"HTTP_WRITE_TIMEOUT":             "9s",
		"HTTP_IDLE_TIMEOUT":              "45s",
		"HTTP_SHUTDOWN_TIMEOUT":          "4s",
		"HTTP_REQUEST_TIMEOUT":           "6s",
		"HTTP_MAX_BODY_BYTES":            "8192",
		"HTTP_MAX_IDEMPOTENCY_KEY_BYTES": "64",
		"RATES_BASE_URL":                 "https://example.com",
		"RATES_REQUEST_TIMEOUT":          "3s",
		"RATES_MAX_ATTEMPTS":             "5",
		"RATES_RETRY_DELAY":              "100ms",
		"RATES_MAX_BODY_BYTES":           "2048",
		"WORKER_ATTEMPT_TIMEOUT":         "25s",
		"WORKER_LEASE":                   "40s",
		"WORKER_POLL_INTERVAL":           "75ms",
	} {
		t.Setenv(key, value)
	}
	got, err := Load()
	require.NoError(t, err)
	want := Config{
		DatabaseURL:         "postgres://localhost/fx_quotes",
		DatabasePingTimeout: 2 * time.Second,
		HTTP: HTTPConfig{
			Port:                   4000,
			ReadTimeout:            7 * time.Second,
			WriteTimeout:           9 * time.Second,
			IdleTimeout:            45 * time.Second,
			ShutdownTimeout:        4 * time.Second,
			RequestTimeout:         6 * time.Second,
			MaxBodyBytes:           8192,
			MaxIdempotencyKeyBytes: 64,
		},
		Rates: RatesConfig{
			BaseURL:      "https://example.com",
			Timeout:      3 * time.Second,
			MaxAttempts:  5,
			RetryDelay:   100 * time.Millisecond,
			MaxBodyBytes: 2048,
		},
		Worker: WorkerConfig{
			AttemptTimeout: 25 * time.Second,
			Lease:          40 * time.Second,
			PollInterval:   75 * time.Millisecond,
		},
	}
	assert.Equal(t, want, got)
}

func TestInvalidSettings(t *testing.T) {
	tests := []struct{ key, value string }{
		{key: "DATABASE_PING_TIMEOUT", value: "0s"},
		{key: "HTTP_READ_TIMEOUT", value: "-1s"},
		{key: "HTTP_REQUEST_TIMEOUT", value: "bad"},
		{key: "HTTP_PORT", value: "65536"},
		{key: "HTTP_PORT", value: "0"},
		{key: "RATES_MAX_ATTEMPTS", value: "-1"},
		{key: "RATES_MAX_ATTEMPTS", value: "1.5"},
		{key: "HTTP_MAX_BODY_BYTES", value: "0"},
		{key: "HTTP_MAX_BODY_BYTES", value: "9223372036854775808"},
		{key: "RATES_MAX_BODY_BYTES", value: "9223372036854775807"},
		{key: "HTTP_MAX_IDEMPOTENCY_KEY_BYTES", value: "-1"},
		{key: "HTTP_WRITE_TIMEOUT", value: "1s"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			resetEnv(t)
			t.Setenv(tt.key, tt.value)
			_, err := Load()
			assert.Error(t, err)
		})
	}
}

func TestWorkerLease(t *testing.T) {
	tests := []struct {
		name, lease, attemptTimeout string
		valid                       bool
	}{
		{name: "negative", lease: "-1s", attemptTimeout: "1ns"},
		{name: "zero", lease: "0s", attemptTimeout: "1ns"},
		{name: "below microsecond", lease: "500ns", attemptTimeout: "1ns"},
		{name: "one microsecond", lease: "1us", attemptTimeout: "500ns", valid: true},
		{name: "equal to attempt timeout", lease: "20s", attemptTimeout: "20s"},
		{name: "shorter than attempt timeout", lease: "19s", attemptTimeout: "20s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEnv(t)
			t.Setenv("WORKER_LEASE", tt.lease)
			t.Setenv("WORKER_ATTEMPT_TIMEOUT", tt.attemptTimeout)
			_, err := Load()
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func resetEnv(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../.env.example")
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, _, ok := strings.Cut(line, "="); ok {
			t.Setenv(key, "")
		}
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/fx_quotes")
}
