package dto

import (
	"time"

	"fx-quotes/internal/domain"
)

type EnqueueQuoteRequest struct {
	Pair string `json:"pair"`
}

type QuoteReference struct {
	ID int64 `json:"id"`
}

type QuoteResponse struct {
	ID        int64              `json:"id"`
	Pair      string             `json:"pair"`
	Status    domain.QuoteStatus `json:"status"`
	Price     *float64           `json:"price,omitempty"`
	UpdatedAt *time.Time         `json:"updated_at,omitempty"`
	Error     string             `json:"error,omitempty"`
}
