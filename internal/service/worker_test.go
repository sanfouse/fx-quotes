package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
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
	for _, tc := range []struct {
		name                                             string
		found, cancel                                    bool
		claimErr, fetchErr, writeErr, wantErr            error
		wantFetch, wantComplete, wantFail, wantProcessed bool
	}{
		{name: "empty queue"},
		{name: "claim failed", claimErr: storageErr, wantErr: storageErr},
		{name: "success", found: true, wantFetch: true, wantComplete: true, wantProcessed: true},
		{name: "provider failed", found: true, fetchErr: providerErr, wantFetch: true, wantFail: true, wantProcessed: true},
		{name: "cancelled fetch leaves claim for retry", found: true, cancel: true, wantFetch: true, wantErr: context.Canceled},
		{name: "complete failed", found: true, writeErr: storageErr, wantErr: storageErr, wantFetch: true, wantComplete: true},
		{name: "fail failed", found: true, fetchErr: providerErr, writeErr: storageErr, wantErr: storageErr, wantFetch: true, wantFail: true},
		{name: "claim replaced", found: true, writeErr: domain.ErrClaimLost, wantErr: domain.ErrClaimLost, wantFetch: true, wantComplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queue := &workerQueue{job: domain.QuoteJob{ID: 7, Pair: "EUR/USD", Version: 3}, found: tc.found, claimErr: tc.claimErr, writeErr: tc.writeErr}
			fetched := false
			rates := workerRates(func(_ context.Context, pair string) (float64, error) {
				fetched = true
				if pair != queue.job.Pair {
					t.Fatalf("unexpected pair %s", pair)
				}
				if tc.cancel {
					cancel()
				}
				return 1.1, tc.fetchErr
			})
			processed, err := NewQuoteWorker(queue, rates, config.Defaults().Worker).processNext(ctx)
			if !errors.Is(err, tc.wantErr) || processed != tc.wantProcessed {
				t.Fatalf("processed=%t error=%v; want processed=%t error=%v", processed, err, tc.wantProcessed, tc.wantErr)
			}
			if fetched != tc.wantFetch || queue.completed != tc.wantComplete || queue.failed != tc.wantFail {
				t.Fatalf("fetch=%t complete=%t fail=%t", fetched, queue.completed, queue.failed)
			}
			if queue.completed && queue.price != 1.1 {
				t.Fatalf("unexpected price %v", queue.price)
			}
		})
	}
}
