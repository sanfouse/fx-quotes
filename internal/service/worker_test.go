package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestWorkerProcessNext(t *testing.T) {
	storageErr := errors.New("database unavailable")
	tests := []struct {
		name                        string
		found                       bool
		claimErr, fetchErr, wantErr error
		wantStatus                  domain.QuoteStatus
		wantPrice                   float64
		wantProcessed               bool
	}{
		{name: "empty queue"},
		{name: "storage error", claimErr: storageErr, wantErr: storageErr},
		{name: "success", found: true, wantStatus: domain.QuoteCompleted, wantPrice: 1.1, wantProcessed: true},
		{name: "provider error", found: true, fetchErr: errors.New("provider unavailable"), wantStatus: domain.QuoteFailed, wantProcessed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &workerQueue{found: tt.found, err: tt.claimErr}
			rates := workerRates(func(_ context.Context, pair string) (float64, error) {
				assert.Equal(t, "EUR/USD", pair)
				return 1.1, tt.fetchErr
			})

			processed, err := NewQuoteWorker(queue, rates, config.Defaults().Worker).processNext(t.Context())
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantProcessed, processed)
			assert.Equal(t, tt.wantStatus, queue.status)
			assert.Equal(t, tt.wantPrice, queue.price)
		})
	}
}

func TestWorkerCanceledFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	queue := &workerQueue{found: true}
	rates := workerRates(func(context.Context, string) (float64, error) {
		cancel()
		return 1.1, nil
	})
	processed, err := NewQuoteWorker(queue, rates, config.Defaults().Worker).processNext(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, processed)
	assert.Empty(t, queue.status)
}

type workerQueue struct {
	found  bool
	err    error
	status domain.QuoteStatus
	price  float64
}

func (q *workerQueue) ClaimNext(context.Context, time.Duration) (domain.QuoteJob, bool, error) {
	return domain.QuoteJob{ID: 7, Pair: "EUR/USD", Version: 3}, q.found, q.err
}

func (q *workerQueue) Complete(_ context.Context, _ domain.QuoteJob, price float64) error {
	q.status, q.price = domain.QuoteCompleted, price
	return nil
}

func (q *workerQueue) Fail(context.Context, domain.QuoteJob) error {
	q.status = domain.QuoteFailed
	return nil
}

type workerRates func(context.Context, string) (float64, error)

func (f workerRates) GetRate(ctx context.Context, pair string) (float64, error) {
	return f(ctx, pair)
}
