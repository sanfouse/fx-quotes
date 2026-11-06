package frankfurter

import (
	"context"
	"errors"
	"time"
)

type retryableError struct {
	err error
}

func (e *retryableError) Error() string {
	return e.err.Error()
}

func (e *retryableError) Unwrap() error {
	return e.err
}

func (r *Client) GetRate(ctx context.Context, pair string) (float64, error) {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		rate, err := r.getRate(ctx, pair)
		if _, retryable := errors.AsType[*retryableError](err); !retryable || attempt >= r.settings.MaxAttempts {
			return rate, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(r.settings.RetryDelay):
		}
	}
}
