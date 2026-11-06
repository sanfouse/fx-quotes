package config

import (
	"errors"
	"math"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
)

type HTTPConfig struct {
	Port                   int           `env:"PORT" envDefault:"3000"`
	ReadHeaderTimeout      time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout            time.Duration `env:"READ_TIMEOUT" envDefault:"5s"`
	WriteTimeout           time.Duration `env:"WRITE_TIMEOUT" envDefault:"5s"`
	IdleTimeout            time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout        time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"5s"`
	RequestTimeout         time.Duration `env:"REQUEST_TIMEOUT" envDefault:"3s"`
	MaxBodyBytes           int64         `env:"MAX_BODY_BYTES" envDefault:"4096"`
	MaxIdempotencyKeyBytes int           `env:"MAX_IDEMPOTENCY_KEY_BYTES" envDefault:"128"`
}

type RatesConfig struct {
	BaseURL      string        `env:"BASE_URL" envDefault:"https://api.frankfurter.dev"`
	Timeout      time.Duration `env:"REQUEST_TIMEOUT" envDefault:"5s"`
	MaxAttempts  int           `env:"MAX_ATTEMPTS" envDefault:"3"`
	RetryDelay   time.Duration `env:"RETRY_DELAY" envDefault:"250ms"`
	MaxBodyBytes int64         `env:"MAX_BODY_BYTES" envDefault:"4096"`
}

type WorkerConfig struct {
	AttemptTimeout time.Duration `env:"ATTEMPT_TIMEOUT" envDefault:"20s"`
	Lease          time.Duration `env:"LEASE" envDefault:"30s"`
	PollInterval   time.Duration `env:"POLL_INTERVAL" envDefault:"500ms"`
}

type Config struct {
	DatabaseURL         string        `env:"DATABASE_URL"`
	DatabasePingTimeout time.Duration `env:"DATABASE_PING_TIMEOUT" envDefault:"5s"`
	HTTP                HTTPConfig    `envPrefix:"HTTP_"`
	Rates               RatesConfig   `envPrefix:"RATES_"`
	Worker              WorkerConfig  `envPrefix:"WORKER_"`
}

func Defaults() Config {
	return env.Must(env.ParseAsWithOptions[Config](env.Options{Environment: map[string]string{}}))
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, err
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	baseURL, err := url.Parse(cfg.Rates.BaseURL)
	if err != nil || baseURL.Hostname() == "" ||
		(baseURL.Scheme != "http" && baseURL.Scheme != "https") ||
		baseURL.ForceQuery || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return Config{}, errors.New("RATES_BASE_URL must be an absolute HTTP(S) URL without a query or fragment")
	}
	cfg.Rates.BaseURL = baseURL.String()
	for _, duration := range []time.Duration{
		cfg.DatabasePingTimeout, cfg.HTTP.ReadHeaderTimeout, cfg.HTTP.ReadTimeout,
		cfg.HTTP.WriteTimeout, cfg.HTTP.IdleTimeout, cfg.HTTP.ShutdownTimeout,
		cfg.HTTP.RequestTimeout, cfg.Rates.Timeout, cfg.Rates.RetryDelay,
		cfg.Worker.AttemptTimeout, cfg.Worker.Lease, cfg.Worker.PollInterval,
	} {
		if duration <= 0 {
			return Config{}, errors.New("timeouts and intervals must be positive")
		}
	}
	if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
		return Config{}, errors.New("HTTP_PORT must be between 1 and 65535")
	}
	for _, size := range []int64{cfg.HTTP.MaxBodyBytes, cfg.Rates.MaxBodyBytes} {
		if size <= 0 || size == math.MaxInt64 {
			return Config{}, errors.New("HTTP_MAX_BODY_BYTES and RATES_MAX_BODY_BYTES must be positive and below MaxInt64")
		}
	}
	if cfg.HTTP.MaxIdempotencyKeyBytes <= 0 || cfg.Rates.MaxAttempts <= 0 {
		return Config{}, errors.New("HTTP_MAX_IDEMPOTENCY_KEY_BYTES and RATES_MAX_ATTEMPTS must be positive")
	}
	if cfg.Worker.Lease.Microseconds() <= 0 || cfg.Worker.Lease <= cfg.Worker.AttemptTimeout {
		return Config{}, errors.New("WORKER_LEASE must be at least 1us and longer than WORKER_ATTEMPT_TIMEOUT")
	}
	if cfg.HTTP.RequestTimeout > cfg.HTTP.WriteTimeout {
		return Config{}, errors.New("HTTP_WRITE_TIMEOUT must be at least HTTP_REQUEST_TIMEOUT")
	}
	return cfg, nil
}
