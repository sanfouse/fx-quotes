package service

import (
	"context"

	"fx-quotes/internal/domain"
)

type quoteStore interface {
	Enqueue(ctx context.Context, pair, key string) (int64, error)
	GetByID(ctx context.Context, id int64) (domain.Quote, error)
	GetLatest(ctx context.Context, pair string) (domain.Quote, error)
}

type QuoteService struct {
	quotes quoteStore
}

func NewQuoteService(quotes quoteStore) *QuoteService {
	return &QuoteService{quotes: quotes}
}

func (s *QuoteService) Enqueue(ctx context.Context, pair, key string) (int64, error) {
	pair, err := domain.NormalizePair(pair)
	if err != nil {
		return 0, err
	}
	return s.quotes.Enqueue(ctx, pair, key)
}

func (s *QuoteService) GetByID(ctx context.Context, id int64) (domain.Quote, error) {
	return s.quotes.GetByID(ctx, id)
}

func (s *QuoteService) GetLatest(ctx context.Context, pair string) (domain.Quote, error) {
	pair, err := domain.NormalizePair(pair)
	if err != nil {
		return domain.Quote{}, err
	}
	return s.quotes.GetLatest(ctx, pair)
}
