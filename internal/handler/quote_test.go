package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/service"

	"github.com/gofiber/fiber/v3"
)

func TestQuoteRoutes(t *testing.T) {
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), config.Defaults().HTTP).Routes()
	serverURL, client := startTestServer(t, app)

	cases := []struct {
		name, method, path, body, key string
		omitKey                       bool
		status                        int
		response, location, allow     string
	}{
		{name: "accepted", method: "POST", path: "/quotes", body: `{"pair":" eur/mxn "}`, key: "demo", status: 202, response: `{"id":3}`, location: "/quotes/3"},
		{name: "malformed", method: "POST", path: "/quotes", body: `{"pair":`, key: "demo", status: 400, response: `{"error":"invalid_request"}`},
		{name: "trailing JSON", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"} {}`, key: "demo", status: 400, response: `{"error":"invalid_request"}`},
		{name: "large body", method: "POST", path: "/quotes", body: strings.Repeat(" ", 4097), key: "demo", status: 413, response: `{"error":"Request Entity Too Large"}`},
		{name: "unsupported pair", method: "POST", path: "/quotes", body: `{"pair":"EUR/JPY"}`, key: "demo", status: 400, response: `{"error":"invalid_request"}`},
		{name: "missing key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, omitKey: true, status: 202, response: `{"id":3}`, location: "/quotes/3"},
		{name: "empty key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, status: 202, response: `{"id":3}`, location: "/quotes/3"},
		{name: "HTTP whitespace key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: " \t ", status: 202, response: `{"id":3}`, location: "/quotes/3"},
		{name: "blank key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: "\u00a0", status: 400, response: `{"error":"invalid_request"}`},
		{name: "long key", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: strings.Repeat("a", 129), status: 400, response: `{"error":"invalid_request"}`},
		{name: "key conflict", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, key: "conflict", status: 409, response: `{"error":"idempotency_conflict"}`},
		{name: "pending", method: "GET", path: "/quotes/1", status: 202, response: `{"id":1,"pair":"EUR/MXN","status":"pending"}`},
		{name: "failed", method: "GET", path: "/quotes/2", status: 200, response: `{"id":2,"pair":"EUR/MXN","status":"failed","error":"rate_unavailable"}`},
		{name: "completed", method: "GET", path: "/quotes/3", status: 200, response: `{"id":3,"pair":"EUR/MXN","status":"completed","price":20.95,"updated_at":"2026-11-05T12:00:00Z"}`},
		{name: "unknown ID", method: "GET", path: "/quotes/4", status: 404, response: `{"error":"not_found"}`},
		{name: "invalid ID", method: "GET", path: "/quotes/0", status: 400, response: `{"error":"invalid_request"}`},
		{name: "non-numeric ID", method: "GET", path: "/quotes/nope", status: 400, response: `{"error":"invalid_request"}`},
		{name: "overflow ID", method: "GET", path: "/quotes/9223372036854775808", status: 400, response: `{"error":"invalid_request"}`},
		{name: "internal error", method: "GET", path: "/quotes/5", status: 500, response: `{"error":"internal_error"}`},
		{name: "panic", method: "GET", path: "/quotes/6", status: 500, response: `{"error":"internal_error"}`},
		{name: "latest", method: "GET", path: "/quotes/latest?pair=%20eur%2Fmxn%20", status: 200, response: `{"id":3,"pair":"EUR/MXN","status":"completed","price":20.95,"updated_at":"2026-11-05T12:00:00Z"}`},
		{name: "latest with uppercase and trailing slash", method: "GET", path: "/QUOTES/LATEST/?pair=EUR%2FMXN", status: 404, response: `{"error":"Not Found"}`},
		{name: "uppercase path", method: "GET", path: "/QUOTES/latest?pair=EUR%2FMXN", status: 404, response: `{"error":"Not Found"}`},
		{name: "trailing slash", method: "GET", path: "/quotes/latest/?pair=EUR%2FMXN", status: 404, response: `{"error":"Not Found"}`},
		{name: "latest missing", method: "GET", path: "/quotes/latest?pair=USD%2FMXN", status: 404, response: `{"error":"not_found"}`},
		{name: "pair missing", method: "GET", path: "/quotes/latest", status: 400, response: `{"error":"invalid_request"}`},
		{name: "unsupported method", method: "POST", path: "/quotes/latest", status: 405, response: `{"error":"Method Not Allowed"}`, allow: "GET"},
		{name: "get enqueue", method: "GET", path: "/quotes", status: 405, response: `{"error":"Method Not Allowed"}`, allow: "POST"},
		{name: "head quote", method: "HEAD", path: "/quotes/3", status: 405, response: `{"error":"Method Not Allowed"}`, allow: "GET"},
		{name: "unknown path", method: "GET", path: "/quotes/3/extra", status: 404, response: `{"error":"Not Found"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, serverURL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
				if !tc.omitKey {
					req.Header.Set("Idempotency-Key", tc.key)
				}
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.status || resp.Header.Get("Location") != tc.location {
				t.Errorf("got status %d, Location %q, want %d, %q", resp.StatusCode, resp.Header.Get("Location"), tc.status, tc.location)
			}
			if got := resp.Header.Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if tc.method == http.MethodHead {
				return
			}

			var got, want map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.response), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestOversizedRequestClosesConnection(t *testing.T) {
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), config.Defaults().HTTP).Routes()
	serverURL, client := startTestServer(t, app)
	resp, err := client.Post(serverURL+"/quotes", "application/json", strings.NewReader(strings.Repeat(" ", 4097)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !resp.Close {
		t.Errorf("got status %d, connection close %t, want 413 and connection close", resp.StatusCode, resp.Close)
	}
}

func startTestServer(t *testing.T, app *fiber.App) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		if err := app.ShutdownWithTimeout(time.Second); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return "http://" + listener.Addr().String(), client
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

func TestConfiguredLimits(t *testing.T) {
	settings := config.Defaults().HTTP
	settings.MaxBodyBytes = 32
	settings.MaxIdempotencyKeyBytes = 4
	app := NewQuoteHandler(service.NewQuoteService(quoteRepoStub{}), settings).Routes()
	serverURL, client := startTestServer(t, app)
	for _, tc := range []struct {
		body, key string
		status    int
	}{
		{`{"pair":"EUR/MXN"}`, "demo", 202},
		{`{"pair":"EUR/MXN"}`, "abcde", 400},
		{strings.Repeat(" ", 33), "demo", 413},
	} {
		req, err := http.NewRequest(http.MethodPost, serverURL+"/quotes", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", tc.key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("got status %d, want %d", resp.StatusCode, tc.status)
		}
	}
}

func TestRequestContext(t *testing.T) {
	for _, waitForTimeout := range []bool{false, true} {
		t.Run(fmt.Sprint("wait for timeout ", waitForTimeout), func(t *testing.T) {
			settings := config.Defaults().HTTP
			settings.RequestTimeout = 20 * time.Millisecond
			repo := &contextQuoteRepo{waitForTimeout: waitForTimeout}
			app := NewQuoteHandler(service.NewQuoteService(repo), settings).Routes()
			started := time.Now()
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/quotes/3", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			deadline, ok := repo.ctx.Deadline()
			if !ok || deadline.Before(started.Add(settings.RequestTimeout)) || deadline.After(time.Now().Add(settings.RequestTimeout)) {
				t.Fatalf("request deadline = %v, set = %t", deadline, ok)
			}
			wantStatus, wantErr := http.StatusOK, context.Canceled
			if waitForTimeout {
				wantStatus, wantErr = http.StatusInternalServerError, context.DeadlineExceeded
			}
			if resp.StatusCode != wantStatus || !errors.Is(repo.ctx.Err(), wantErr) {
				t.Fatalf("status = %d, context error = %v; want %d, %v", resp.StatusCode, repo.ctx.Err(), wantStatus, wantErr)
			}
		})
	}
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
