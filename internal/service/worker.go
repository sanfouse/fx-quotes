package service

import (
	"context"
	"log/slog"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
)

type quoteQueue interface {
	ClaimNext(ctx context.Context, lease time.Duration) (domain.QuoteJob, bool, error)
	Complete(ctx context.Context, job domain.QuoteJob, price float64) error
	Fail(ctx context.Context, job domain.QuoteJob) error
}

type rateProvider interface {
	GetRate(ctx context.Context, pair string) (float64, error)
}

type QuoteWorker struct {
	queue    quoteQueue
	rates    rateProvider
	settings config.WorkerConfig
}

func NewQuoteWorker(queue quoteQueue, rates rateProvider, settings config.WorkerConfig) *QuoteWorker {
	return &QuoteWorker{queue: queue, rates: rates, settings: settings}
}

func (w *QuoteWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, w.settings.AttemptTimeout)
		processed, err := w.processNext(attemptCtx)
		cancel()

		if err != nil {
			slog.Error("process quote", "error", err)
		} else if processed {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(w.settings.PollInterval):
		}
	}
}

func (w *QuoteWorker) processNext(ctx context.Context) (bool, error) {
	job, found, err := w.queue.ClaimNext(ctx, w.settings.Lease)
	if err != nil || !found {
		return false, err
	}

	price, fetchErr := w.rates.GetRate(ctx, job.Pair)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if fetchErr != nil {
		slog.WarnContext(ctx, "fetch exchange rate", "quote_id", job.ID, "pair", job.Pair, "error", fetchErr)
		if err := w.queue.Fail(ctx, job); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := w.queue.Complete(ctx, job, price); err != nil {
		return false, err
	}
	return true, nil
}
