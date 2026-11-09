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

type workerQueue struct {
	job       domain.QuoteJob
	found     bool
	claimErr  error
	writeErr  error
	completed bool
	failed    bool
	price     float64
}

func (q *workerQueue) ClaimNext(_ context.Context, lease time.Duration) (domain.QuoteJob, bool, error) {
	if lease <= config.Defaults().Worker.AttemptTimeout {
		panic("lease must outlast attempt timeout")
	}
	return q.job, q.found, q.claimErr
}

func (q *workerQueue) Complete(_ context.Context, job domain.QuoteJob, price float64) error {
	if job != q.job {
		panic("incorrect claim")
	}
	q.completed, q.price = true, price
	return q.writeErr
}

func (q *workerQueue) Fail(_ context.Context, job domain.QuoteJob) error {
	if job != q.job {
		panic("incorrect claim")
	}
	q.failed = true
	return q.writeErr
}

type workerRates func(context.Context, string) (float64, error)

func (f workerRates) GetRate(ctx context.Context, pair string) (float64, error) { return f(ctx, pair) }

func TestWorkerProcessNext(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	storageErr := errors.New("database unavailable")
	tests := []struct {
		name                                             string
		found                                            bool
		claimErr, fetchErr, writeErr, wantErr            error
		wantFetch, wantComplete, wantFail, wantProcessed bool
	}{
		{name: "empty queue"},
		{name: "claim failed", claimErr: storageErr, wantErr: storageErr},
		{name: "success", found: true, wantFetch: true, wantComplete: true, wantProcessed: true},
		{name: "provider failed", found: true, fetchErr: providerErr, wantFetch: true, wantFail: true, wantProcessed: true},
		{name: "complete failed", found: true, writeErr: storageErr, wantErr: storageErr, wantFetch: true, wantComplete: true},
		{name: "fail failed", found: true, fetchErr: providerErr, writeErr: storageErr, wantErr: storageErr, wantFetch: true, wantFail: true},
		{name: "claim replaced", found: true, writeErr: domain.ErrClaimLost, wantErr: domain.ErrClaimLost, wantFetch: true, wantComplete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &workerQueue{
				job:      domain.QuoteJob{ID: 7, Pair: "EUR/USD", Version: 3},
				found:    tt.found,
				claimErr: tt.claimErr,
				writeErr: tt.writeErr,
			}
			fetched := false
			rates := workerRates(func(_ context.Context, pair string) (float64, error) {
				fetched = true
				assert.Equal(t, queue.job.Pair, pair)
				return 1.1, tt.fetchErr
			})

			processed, err := NewQuoteWorker(queue, rates, config.Defaults().Worker).processNext(t.Context())
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantProcessed, processed)
			assert.Equal(t, tt.wantFetch, fetched, "provider called")
			assert.Equal(t, tt.wantComplete, queue.completed, "quote completed")
			assert.Equal(t, tt.wantFail, queue.failed, "quote failed")
			if queue.completed {
				assert.Equal(t, 1.1, queue.price)
			}
		})
	}
}

func TestWorkerCanceledFetchLeavesClaimForRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	queue := &workerQueue{job: domain.QuoteJob{ID: 7, Pair: "EUR/USD", Version: 3}, found: true}
	fetched := false
	rates := workerRates(func(_ context.Context, pair string) (float64, error) {
		fetched = true
		assert.Equal(t, queue.job.Pair, pair)
		cancel()
		return 1.1, nil
	})

	processed, err := NewQuoteWorker(queue, rates, config.Defaults().Worker).processNext(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, processed)
	assert.True(t, fetched)
	assert.False(t, queue.completed)
	assert.False(t, queue.failed)
}
