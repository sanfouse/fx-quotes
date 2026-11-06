package frankfurter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"fx-quotes/internal/config"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	settings   config.RatesConfig
}

func NewClient(baseURL string, client *http.Client, settings config.RatesConfig) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: client, settings: settings}
}

func (r *Client) getRate(ctx context.Context, pair string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v2/rate/"+pair, nil)
	if err != nil {
		return 0, err
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, &retryableError{err: err}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("close Frankfurter response body", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		status := resp.StatusCode
		err := fmt.Errorf("unexpected HTTP status %d from Frankfurter", status)
		if status >= 500 && status < 600 {
			return 0, &retryableError{err: err}
		}
		return 0, err
	}

	var rate struct {
		Base  string  `json:"base"`
		Quote string  `json:"quote"`
		Rate  float64 `json:"rate"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, r.settings.MaxBodyBytes+1))
	if err != nil {
		return 0, &retryableError{err: err}
	}
	if int64(len(body)) > r.settings.MaxBodyBytes {
		return 0, errors.New("response body from Frankfurter is too large")
	}
	if err := json.Unmarshal(body, &rate); err != nil {
		return 0, fmt.Errorf("decode Frankfurter response: %w", err)
	}
	base, quote, _ := strings.Cut(pair, "/")
	if rate.Base != base || rate.Quote != quote || rate.Rate <= 0 {
		return 0, fmt.Errorf("invalid Frankfurter rate for %s", pair)
	}
	return rate.Rate, nil
}
