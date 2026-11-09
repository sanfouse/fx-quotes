package postgres

import (
	"testing"
	"time"

	"fx-quotes/internal/db"
	"fx-quotes/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueue(t *testing.T) {
	tests := []struct {
		name, existingPair string
		wantID             int64
		wantErr            error
	}{
		{name: "new quote", wantID: 3},
		{name: "repeated key", existingPair: "EUR/MXN", wantID: 3},
		{name: "key conflict", existingPair: "USD/MXN", wantErr: domain.ErrIdempotencyConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, pool := newTestRepo(t)
			key := pgtype.Text{String: "demo", Valid: true}
			insert := pool.ExpectQuery("INSERT INTO quotes").WithArgs("EUR/MXN", key)
			if tt.existingPair == "" {
				insert.WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(3)))
			} else {
				insert.WillReturnError(pgx.ErrNoRows)
				pool.ExpectQuery("SELECT id, pair FROM quotes").WithArgs(key).
					WillReturnRows(pgxmock.NewRows([]string{"id", "pair"}).AddRow(int64(3), tt.existingPair))
			}

			id, err := repo.Enqueue(t.Context(), "EUR/MXN", "demo")
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantID, id)
		})
	}
}

func TestGetLatest(t *testing.T) {
	repo, pool := newTestRepo(t)
	updatedAt := time.Date(2026, time.November, 5, 12, 0, 0, 0, time.UTC)
	rows := pgxmock.NewRows([]string{"id", "pair", "idempotency_key", "status", "price", "updated_at", "claim_version", "lease_until"}).
		AddRow(int64(3), "EUR/MXN", nil, "completed", 20.95, updatedAt, int64(1), nil)
	pool.ExpectQuery("ORDER BY updated_at DESC").WithArgs("EUR/MXN").WillReturnRows(rows)

	got, err := repo.GetLatest(t.Context(), "EUR/MXN")
	require.NoError(t, err)
	assert.Equal(t, domain.Quote{ID: 3, Pair: "EUR/MXN", Status: domain.QuoteCompleted, Price: 20.95, UpdatedAt: updatedAt}, got)
}

func TestGetByIDNotFound(t *testing.T) {
	repo, pool := newTestRepo(t)
	pool.ExpectQuery(`WHERE id = \$1`).WithArgs(int64(3)).WillReturnError(pgx.ErrNoRows)
	_, err := repo.GetByID(t.Context(), 3)
	assert.ErrorIs(t, err, domain.ErrQuoteNotFound)
}

func TestClaimNext(t *testing.T) {
	repo, pool := newTestRepo(t)
	pool.ExpectQuery("WITH next AS").WithArgs(int64(30_000_000)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pair", "claim_version"}).AddRow(int64(3), "EUR/MXN", int64(1)))

	job, found, err := repo.ClaimNext(t.Context(), 30*time.Second)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, domain.QuoteJob{ID: 3, Pair: "EUR/MXN", Version: 1}, job)

	pool.ExpectQuery("WITH next AS").WithArgs(int64(30_000_000)).WillReturnError(pgx.ErrNoRows)
	_, found, err = repo.ClaimNext(t.Context(), 30*time.Second)
	assert.NoError(t, err)
	assert.False(t, found)
}

func TestFinishClaim(t *testing.T) {
	tests := []struct {
		name     string
		complete bool
		rows     int64
		wantErr  error
	}{
		{name: "complete", complete: true, rows: 1},
		{name: "fail", rows: 1},
		{name: "complete lost claim", complete: true, wantErr: domain.ErrClaimLost},
		{name: "fail lost claim", wantErr: domain.ErrClaimLost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, pool := newTestRepo(t)
			job := domain.QuoteJob{ID: 3, Pair: "EUR/MXN", Version: 1}
			var err error
			if tt.complete {
				pool.ExpectExec("UPDATE quotes SET status = 'completed'").
					WithArgs(job.ID, pgtype.Float8{Float64: 20.95, Valid: true}, job.Version).
					WillReturnResult(pgxmock.NewResult("UPDATE", tt.rows))
				err = repo.Complete(t.Context(), job, 20.95)
			} else {
				pool.ExpectExec("UPDATE quotes SET status = 'failed'").WithArgs(job.ID, job.Version).
					WillReturnResult(pgxmock.NewResult("UPDATE", tt.rows))
				err = repo.Fail(t.Context(), job)
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func newTestRepo(t *testing.T) (*QuoteRepo, pgxmock.PgxPoolIface) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	t.Cleanup(func() { assert.NoError(t, pool.ExpectationsWereMet()) })
	return &QuoteRepo{queries: db.New(pool)}, pool
}
