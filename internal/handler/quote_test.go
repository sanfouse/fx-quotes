package handler

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueQuote(t *testing.T) {
	tests := []quoteRouteTest{
		{name: "accepted", method: "POST", path: "/quotes", body: `{"pair":" eur/mxn "}`, key: "demo", wantStatus: 202, wantBody: `{"id":3}`, wantLocation: "/quotes/3"},
		{name: "missing key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, omitKey: true, wantStatus: 202, wantBody: `{"id":3}`, wantLocation: "/quotes/3"},
		{name: "empty key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, wantStatus: 202, wantBody: `{"id":3}`, wantLocation: "/quotes/3"},
		{name: "HTTP whitespace key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: " \t ", wantStatus: 202, wantBody: `{"id":3}`, wantLocation: "/quotes/3"},
		{name: "malformed", method: "POST", path: "/quotes", body: `{"pair":`, key: "demo", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "trailing JSON", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"} {}`, key: "demo", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "large body", method: "POST", path: "/quotes", body: strings.Repeat(" ", 4097), key: "demo", wantStatus: 413, wantBody: `{"error":"Request Entity Too Large"}`},
		{name: "unsupported pair", method: "POST", path: "/quotes", body: `{"pair":"EUR/JPY"}`, key: "demo", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "blank key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: "\u00a0", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "long key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: strings.Repeat("a", 129), wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "key conflict", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: "conflict", wantStatus: 409, wantBody: `{"error":"idempotency_conflict"}`},
	}
	testQuoteRoutes(t, tests)
}

func TestGetQuoteByID(t *testing.T) {
	tests := []quoteRouteTest{
		{name: "pending", method: "GET", path: "/quotes/1", wantStatus: 202, wantBody: `{"id":1,"pair":"EUR/MXN","status":"pending"}`},
		{name: "failed", method: "GET", path: "/quotes/2", wantStatus: 200, wantBody: `{"id":2,"pair":"EUR/MXN","status":"failed","error":"rate_unavailable"}`},
		{name: "completed", method: "GET", path: "/quotes/3", wantStatus: 200, wantBody: `{"id":3,"pair":"EUR/MXN","status":"completed","price":20.95,"updated_at":"2026-11-05T12:00:00Z"}`},
		{name: "unknown ID", method: "GET", path: "/quotes/4", wantStatus: 404, wantBody: `{"error":"not_found"}`},
		{name: "invalid ID", method: "GET", path: "/quotes/0", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "non-numeric ID", method: "GET", path: "/quotes/nope", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "overflow ID", method: "GET", path: "/quotes/9223372036854775808", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "internal error", method: "GET", path: "/quotes/5", wantStatus: 500, wantBody: `{"error":"internal_error"}`},
		{name: "panic", method: "GET", path: "/quotes/6", wantStatus: 500, wantBody: `{"error":"internal_error"}`},
	}
	testQuoteRoutes(t, tests)
}

func TestGetLatestQuote(t *testing.T) {
	tests := []quoteRouteTest{
		{name: "latest", method: "GET", path: "/quotes/latest?pair=%20eur%2Fmxn%20", wantStatus: 200, wantBody: `{"id":3,"pair":"EUR/MXN","status":"completed","price":20.95,"updated_at":"2026-11-05T12:00:00Z"}`},
		{name: "latest missing", method: "GET", path: "/quotes/latest?pair=USD%2FMXN", wantStatus: 404, wantBody: `{"error":"not_found"}`},
		{name: "pair missing", method: "GET", path: "/quotes/latest", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
	}
	testQuoteRoutes(t, tests)
}

func TestQuoteRouting(t *testing.T) {
	tests := []quoteRouteTest{
		{name: "uppercase and trailing slash", method: "GET", path: "/QUOTES/LATEST/?pair=EUR%2FMXN", wantStatus: 404, wantBody: `{"error":"Not Found"}`},
		{name: "uppercase path", method: "GET", path: "/QUOTES/latest?pair=EUR%2FMXN", wantStatus: 404, wantBody: `{"error":"Not Found"}`},
		{name: "trailing slash", method: "GET", path: "/quotes/latest/?pair=EUR%2FMXN", wantStatus: 404, wantBody: `{"error":"Not Found"}`},
		{name: "unsupported method", method: "POST", path: "/quotes/latest", wantStatus: 405, wantBody: `{"error":"Method Not Allowed"}`, wantAllow: "GET"},
		{name: "get enqueue", method: "GET", path: "/quotes", wantStatus: 405, wantBody: `{"error":"Method Not Allowed"}`, wantAllow: "POST"},
		{name: "head quote", method: "HEAD", path: "/quotes/3", wantStatus: 405, wantAllow: "GET"},
		{name: "unknown path", method: "GET", path: "/quotes/3/extra", wantStatus: 404, wantBody: `{"error":"Not Found"}`},
	}
	testQuoteRoutes(t, tests)
}

func TestOversizedRequestClosesConnection(t *testing.T) {
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), config.Defaults().HTTP).Routes()
	serverURL, client := startTestServer(t, app)
	resp, err := client.Post(serverURL+"/quotes", "application/json", strings.NewReader(strings.Repeat(" ", 4097)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.True(t, resp.Close, "oversized request closes connection")
}

func TestConfiguredLimits(t *testing.T) {
	settings := config.Defaults().HTTP
	settings.MaxBodyBytes = 32
	settings.MaxIdempotencyKeyBytes = 4
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), settings).Routes()
	serverURL, client := startTestServer(t, app)
	tests := []struct {
		name, body, key string
		wantStatus      int
	}{
		{name: "within limits", body: `{"pair":"EUR/MXN"}`, key: "demo", wantStatus: 202},
		{name: "long key", body: `{"pair":"EUR/MXN"}`, key: "abcde", wantStatus: 400},
		{name: "large body", body: strings.Repeat(" ", 33), key: "demo", wantStatus: 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, serverURL+"/quotes", strings.NewReader(tt.body))
			require.NoError(t, err)
			req.Header.Set("Idempotency-Key", tt.key)
			resp, _ := sendRequest(t, client, req)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestRequestContext(t *testing.T) {
	tests := []struct {
		name           string
		waitForTimeout bool
		wantStatus     int
		wantErr        error
	}{
		{name: "canceled after response", wantStatus: http.StatusOK, wantErr: context.Canceled},
		{name: "deadline exceeded", waitForTimeout: true, wantStatus: http.StatusInternalServerError, wantErr: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := config.Defaults().HTTP
			settings.RequestTimeout = 20 * time.Millisecond
			repo := &contextQuoteRepo{waitForTimeout: tt.waitForTimeout}
			app := NewQuoteHandler(service.NewQuoteService(repo), settings).Routes()
			started := time.Now()
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/quotes/3", nil))
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			deadline, ok := repo.ctx.Deadline()
			require.True(t, ok, "request context has deadline")
			assert.WithinRange(t, deadline, started.Add(settings.RequestTimeout), time.Now().Add(settings.RequestTimeout))
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.ErrorIs(t, repo.ctx.Err(), tt.wantErr)
		})
	}
}

type quoteRouteTest struct {
	name, method, path, body, key     string
	omitKey                           bool
	wantStatus                        int
	wantBody, wantLocation, wantAllow string
}

func testQuoteRoutes(t *testing.T, tests []quoteRouteTest) {
	t.Helper()
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), config.Defaults().HTTP).Routes()
	serverURL, client := startTestServer(t, app)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, serverURL+tt.path, strings.NewReader(tt.body))
			require.NoError(t, err)
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
				if !tt.omitKey {
					req.Header.Set("Idempotency-Key", tt.key)
				}
			}

			resp, body := sendRequest(t, client, req)

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, tt.wantLocation, resp.Header.Get("Location"))
			assert.Equal(t, tt.wantAllow, resp.Header.Get("Allow"))
			assert.Regexp(t, "^application/json", resp.Header.Get("Content-Type"))
			if tt.method == http.MethodHead {
				assert.Empty(t, body)
				return
			}
			assert.JSONEq(t, tt.wantBody, body)
		})
	}
}

func startTestServer(t *testing.T, app *fiber.App) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		assert.NoError(t, app.ShutdownWithTimeout(time.Second))
		assert.NoError(t, <-done)
	})
	return "http://" + listener.Addr().String(), client
}

func sendRequest(t *testing.T, client *http.Client, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp, readResponse(t, resp)
}

func readResponse(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

type quoteRepoStub struct{}

func (quoteRepoStub) Enqueue(_ context.Context, pair, key string) (int64, error) {
	if pair == "EUR/MXN" && (key == "demo" || key == "") {
		return 3, nil
	}
	if key == "conflict" {
		return 0, domain.ErrIdempotencyConflict
	}
	return 0, errors.New("unexpected enqueue")
}

func (quoteRepoStub) GetByID(_ context.Context, id int64) (domain.Quote, error) {
	switch id {
	case 1:
		return domain.Quote{ID: id, Pair: "EUR/MXN", Status: domain.QuotePending}, nil
	case 2:
		return domain.Quote{ID: id, Pair: "EUR/MXN", Status: domain.QuoteFailed}, nil
	case 3:
		return domain.Quote{
			ID: 3, Pair: "EUR/MXN", Status: domain.QuoteCompleted, Price: 20.95,
			UpdatedAt: time.Date(2026, time.November, 5, 12, 0, 0, 0, time.UTC),
		}, nil
	case 5:
		return domain.Quote{}, errors.New("database unavailable")
	case 6:
		panic("unexpected database failure")
	default:
		return domain.Quote{}, domain.ErrQuoteNotFound
	}
}

func (r quoteRepoStub) GetLatest(ctx context.Context, pair string) (domain.Quote, error) {
	if pair == "EUR/MXN" {
		return r.GetByID(ctx, 3)
	}
	return domain.Quote{}, domain.ErrQuoteNotFound
}

type contextQuoteRepo struct {
	quoteRepoStub
	ctx            context.Context
	waitForTimeout bool
}

func (r *contextQuoteRepo) GetByID(ctx context.Context, id int64) (domain.Quote, error) {
	r.ctx = ctx
	if r.waitForTimeout {
		<-ctx.Done()
		return domain.Quote{}, ctx.Err()
	}
	return r.quoteRepoStub.GetByID(ctx, id)
}
