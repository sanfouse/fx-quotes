package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fx-quotes/internal/db"
	"fx-quotes/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type QuoteRepo struct {
	queries *db.Queries
}

func NewQuoteRepo(pool *pgxpool.Pool) *QuoteRepo {
	return &QuoteRepo{queries: db.New(pool)}
}

func (r *QuoteRepo) Enqueue(ctx context.Context, pair, key string) (int64, error) {
	param := pgtype.Text{String: key, Valid: key != ""}
	id, err := r.queries.EnqueueQuote(ctx, db.EnqueueQuoteParams{Pair: pair, IdempotencyKey: param})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("enqueue quote for %s: %w", pair, err)
	}

	existing, err := r.queries.GetQuoteByKey(ctx, param)
	if err != nil {
		return 0, fmt.Errorf("find idempotent quote for %s: %w", pair, err)
	}
	if existing.Pair != pair {
		return 0, domain.ErrIdempotencyConflict
	}
	return existing.ID, nil
}

func (r *QuoteRepo) GetByID(ctx context.Context, id int64) (domain.Quote, error) {
	row, err := r.queries.GetQuote(ctx, id)
	quote, err := toDomainQuote(row, err)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("get quote %d: %w", id, err)
	}
	return quote, nil
}

func (r *QuoteRepo) GetLatest(ctx context.Context, pair string) (domain.Quote, error) {
	row, err := r.queries.GetLatestQuote(ctx, pair)
	quote, err := toDomainQuote(row, err)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("get latest quote for %s: %w", pair, err)
	}
	return quote, nil
}

func (r *QuoteRepo) ClaimNext(ctx context.Context, lease time.Duration) (domain.QuoteJob, bool, error) {
	row, err := r.queries.ClaimNextQuote(ctx, lease.Microseconds())
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.QuoteJob{}, false, nil
	}
	if err != nil {
		return domain.QuoteJob{}, false, fmt.Errorf("claim next quote: %w", err)
	}
	return domain.QuoteJob{ID: row.ID, Pair: row.Pair, Version: row.ClaimVersion}, true, nil
}

func (r *QuoteRepo) Complete(ctx context.Context, job domain.QuoteJob, price float64) error {
	rows, err := r.queries.CompleteQuote(ctx, db.CompleteQuoteParams{
		ID:           job.ID,
		Price:        pgtype.Float8{Float64: price, Valid: true},
		ClaimVersion: job.Version,
	})
	if err != nil {
		return fmt.Errorf("complete quote %d: %w", job.ID, err)
	}
	if rows == 0 {
		return domain.ErrClaimLost
	}
	return nil
}

func (r *QuoteRepo) Fail(ctx context.Context, job domain.QuoteJob) error {
	rows, err := r.queries.FailQuote(ctx, db.FailQuoteParams{ID: job.ID, ClaimVersion: job.Version})
	if err != nil {
		return fmt.Errorf("fail quote %d: %w", job.ID, err)
	}
	if rows == 0 {
		return domain.ErrClaimLost
	}
	return nil
}

func toDomainQuote(row db.Quote, err error) (domain.Quote, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Quote{}, domain.ErrQuoteNotFound
	}
	if err != nil {
		return domain.Quote{}, err
	}
	return domain.Quote{
		ID:        row.ID,
		Pair:      row.Pair,
		Status:    domain.QuoteStatus(row.Status),
		Price:     row.Price.Float64,
		UpdatedAt: row.UpdatedAt.Time.UTC(),
	}, nil
}
