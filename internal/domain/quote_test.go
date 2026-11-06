package domain

import (
	"errors"
	"testing"
)

func TestNormalizePair(t *testing.T) {
	for _, pair := range []string{"EUR/MXN", "EUR/USD", "MXN/EUR", "MXN/USD", "USD/EUR", "USD/MXN"} {
		got, err := NormalizePair(pair)
		if err != nil || got != pair {
			t.Errorf("NormalizePair(%q) = %q, %v", pair, got, err)
		}
	}

	got, err := NormalizePair(" eur/mxn ")
	if err != nil || got != "EUR/MXN" {
		t.Errorf("NormalizePair(%q) = %q, %v", " eur/mxn ", got, err)
	}

	for _, pair := range []string{"", "EUR", "EUR/EUR", "EUR/GBP", "EUR /MXN", "EUR/MXN/USD"} {
		if _, err := NormalizePair(pair); !errors.Is(err, ErrInvalidPair) {
			t.Errorf("NormalizePair(%q) returned %v, want ErrInvalidPair", pair, err)
		}
	}
}
