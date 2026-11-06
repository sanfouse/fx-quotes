package db

import (
	"github.com/jackc/pgx/v5/pgtype"
)

type Quote struct {
	ID             int64
	Pair           string
	IdempotencyKey pgtype.Text
	Status         string
	Price          pgtype.Float8
	UpdatedAt      pgtype.Timestamptz
	ClaimVersion   int64
	LeaseUntil     pgtype.Timestamptz
}
