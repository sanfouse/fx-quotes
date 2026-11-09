package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	resetEnv(t)
	const databaseURL = "postgres://localhost/fx_quotes"
	cases := []struct {
		name, databaseURL, ratesURL, wantRatesURL string
	}{
		{name: "default provider", databaseURL: databaseURL, wantRatesURL: "https://api.frankfurter.dev"},
		{name: "custom provider", databaseURL: databaseURL, ratesURL: "http://localhost:8080/api/", wantRatesURL: "http://localhost:8080/api/"},
		{name: "missing database URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tc.databaseURL)
			t.Setenv("RATES_BASE_URL", tc.ratesURL)
			cfg, err := Load()
			if tc.wantRatesURL == "" {
				if err == nil {
					t.Fatal("invalid configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DatabaseURL != tc.databaseURL || cfg.Rates.BaseURL != tc.wantRatesURL {
				t.Fatalf("Load() = %+v, want database URL %q and rates URL %q", cfg, tc.databaseURL, tc.wantRatesURL)
			}
		})
	}
}

func resetEnv(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, _, ok := strings.Cut(line, "="); ok {
			t.Setenv(key, "")
		}
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/fx_quotes")
}

func TestExampleConfig(t *testing.T) {
	resetEnv(t)
	data, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			t.Setenv(key, strings.Trim(value, "'"))
		}
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.DatabaseURL = os.Getenv("DATABASE_URL")
	if got != want {
		t.Fatalf("example configuration = %+v, defaults = %+v", got, want)
	}
}

func TestSettingsOverrides(t *testing.T) {
	resetEnv(t)
	for key, value := range map[string]string{
		"DATABASE_PING_TIMEOUT": "2s",
		"HTTP_PORT":             "4000", "HTTP_READ_HEADER_TIMEOUT": "1s",
		"HTTP_READ_TIMEOUT": "7s", "HTTP_WRITE_TIMEOUT": "9s",
		"HTTP_IDLE_TIMEOUT": "45s", "HTTP_SHUTDOWN_TIMEOUT": "4s",
		"HTTP_REQUEST_TIMEOUT": "6s", "HTTP_MAX_BODY_BYTES": "8192",
		"HTTP_MAX_IDEMPOTENCY_KEY_BYTES": "64",
		"RATES_BASE_URL":                 "https://example.com", "RATES_REQUEST_TIMEOUT": "3s",
		"RATES_MAX_ATTEMPTS": "5", "RATES_RETRY_DELAY": "100ms", "RATES_MAX_BODY_BYTES": "2048",
		"WORKER_ATTEMPT_TIMEOUT": "25s", "WORKER_LEASE": "40s", "WORKER_POLL_INTERVAL": "75ms",
	} {
		t.Setenv(key, value)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		DatabaseURL: "postgres://localhost/fx_quotes", DatabasePingTimeout: 2 * time.Second,
		HTTP: HTTPConfig{
			Port: 4000, ReadHeaderTimeout: time.Second, ReadTimeout: 7 * time.Second,
			WriteTimeout: 9 * time.Second, IdleTimeout: 45 * time.Second,
			ShutdownTimeout: 4 * time.Second, RequestTimeout: 6 * time.Second,
			MaxBodyBytes: 8192, MaxIdempotencyKeyBytes: 64,
		},
		Rates:  RatesConfig{BaseURL: "https://example.com", Timeout: 3 * time.Second, MaxAttempts: 5, RetryDelay: 100 * time.Millisecond, MaxBodyBytes: 2048},
		Worker: WorkerConfig{AttemptTimeout: 25 * time.Second, Lease: 40 * time.Second, PollInterval: 75 * time.Millisecond},
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_PING_TIMEOUT", "0s"}, {"HTTP_READ_TIMEOUT", "-1s"},
		{"HTTP_REQUEST_TIMEOUT", "bad"}, {"HTTP_PORT", "65536"},
		{"HTTP_PORT", "0"}, {"RATES_MAX_ATTEMPTS", "-1"},
		{"RATES_MAX_ATTEMPTS", "1.5"}, {"HTTP_MAX_BODY_BYTES", "0"},
		{"HTTP_MAX_BODY_BYTES", "9223372036854775808"},
		{"RATES_MAX_BODY_BYTES", "9223372036854775807"},
		{"HTTP_MAX_IDEMPOTENCY_KEY_BYTES", "-1"},
		{"HTTP_WRITE_TIMEOUT", "1s"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			resetEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestWorkerLease(t *testing.T) {
	for _, tc := range []struct {
		name, lease, attemptTimeout string
		valid                       bool
	}{
		{name: "negative", lease: "-1s", attemptTimeout: "1ns"},
		{name: "zero", lease: "0s", attemptTimeout: "1ns"},
		{name: "below microsecond", lease: "500ns", attemptTimeout: "1ns"},
		{name: "one microsecond", lease: "1us", attemptTimeout: "500ns", valid: true},
		{name: "equal to attempt timeout", lease: "20s", attemptTimeout: "20s"},
		{name: "shorter than attempt timeout", lease: "19s", attemptTimeout: "20s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)
			t.Setenv("WORKER_LEASE", tc.lease)
			t.Setenv("WORKER_ATTEMPT_TIMEOUT", tc.attemptTimeout)
			if _, err := Load(); (err == nil) != tc.valid {
				t.Fatalf("Load() error = %v, want valid = %t", err, tc.valid)
			}
		})
	}
}
