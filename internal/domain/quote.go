package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidPair         = errors.New("unsupported currency pair")
	ErrQuoteNotFound       = errors.New("quote not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used for another pair")
	ErrClaimLost           = errors.New("quote claim expired or replaced")
)

type QuoteStatus string

const (
	QuotePending   QuoteStatus = "pending"
	QuoteCompleted QuoteStatus = "completed"
	QuoteFailed    QuoteStatus = "failed"
)

type Quote struct {
	ID        int64
	Pair      string
	Status    QuoteStatus
	Price     float64
	UpdatedAt time.Time
}

type QuoteJob struct {
	ID      int64
	Pair    string
	Version int64
}

func NormalizePair(value string) (string, error) {
	pair := strings.ToUpper(strings.TrimSpace(value))
	switch pair {
	case "EUR/MXN", "EUR/USD", "MXN/EUR", "MXN/USD", "USD/EUR", "USD/MXN":
		return pair, nil
	default:
		return "", ErrInvalidPair
	}
}
