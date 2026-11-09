package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePair(t *testing.T) {
	tests := []struct {
		name, pair, want string
		wantErr          error
	}{
		{name: "supported", pair: "EUR/USD", want: "EUR/USD"},
		{name: "normalized", pair: " eur/mxn ", want: "EUR/MXN"},
		{name: "missing", wantErr: ErrInvalidPair},
		{name: "unsupported", pair: "EUR/GBP", wantErr: ErrInvalidPair},
		{name: "same currency", pair: "EUR/EUR", wantErr: ErrInvalidPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePair(tt.pair)
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.want, got)
		})
	}
}
