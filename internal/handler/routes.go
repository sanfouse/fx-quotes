package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

func (h *QuoteHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/quotes", onlyMethod(h.enqueue, http.MethodPost))
	mux.HandleFunc("/quotes/latest", onlyMethod(h.getLatest, http.MethodGet))
	mux.HandleFunc("/quotes/{id}", onlyMethod(h.getByID, http.MethodGet))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
	})

	return http.MaxBytesHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestPath := r.URL.EscapedPath()
		ctx, cancel := context.WithTimeout(r.Context(), h.settings.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)

		mux.ServeHTTP(w, r)
		slog.InfoContext(ctx, "http request", "method", r.Method, "path", requestPath,
			"duration", time.Since(started))
	}), h.settings.MaxBodyBytes)
}

func onlyMethod(next http.HandlerFunc, method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		next(w, r)
	}
}
