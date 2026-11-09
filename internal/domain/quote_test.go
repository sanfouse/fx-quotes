package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePair(t *testing.T) {
	for _, pair := range []string{"EUR/MXN", "EUR/USD", "MXN/EUR", "MXN/USD", "USD/EUR", "USD/MXN"} {
		t.Run(pair, func(t *testing.T) {
			got, err := NormalizePair(pair)
			require.NoError(t, err)
			assert.Equal(t, pair, got)
		})
	}

	t.Run("trim and uppercase", func(t *testing.T) {
		got, err := NormalizePair(" eur/mxn ")
		require.NoError(t, err)
		assert.Equal(t, "EUR/MXN", got)
	})
}

func TestNormalizePairInvalid(t *testing.T) {
	for _, pair := range []string{"", "EUR", "EUR/EUR", "EUR/GBP", "EUR /MXN", "EUR/MXN/USD"} {
		t.Run(pair, func(t *testing.T) {
			_, err := NormalizePair(pair)
			assert.ErrorIs(t, err, ErrInvalidPair)
		})
	}
}
