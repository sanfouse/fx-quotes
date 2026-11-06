package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/handler/dto"
	"fx-quotes/internal/service"
)

type QuoteHandler struct {
	service  *service.QuoteService
	settings config.HTTPConfig
}

func NewQuoteHandler(service *service.QuoteService, settings config.HTTPConfig) *QuoteHandler {
	return &QuoteHandler{service: service, settings: settings}
}

func (h *QuoteHandler) enqueue(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeError(w, http.StatusRequestEntityTooLarge, http.StatusText(http.StatusRequestEntityTooLarge))
		return
	}
	var req dto.EnqueueQuoteRequest
	if err != nil || json.Unmarshal(body, &req) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key != "" && (strings.TrimSpace(key) == "" || len(key) > h.settings.MaxIdempotencyKeyBytes) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, err := h.service.Enqueue(r.Context(), req.Pair, key)
	if err != nil {
		writeQuoteError(w, r, err)
		return
	}
	w.Header().Set("Location", "/quotes/"+strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusAccepted, dto.QuoteReference{ID: id})
}

func (h *QuoteHandler) getByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	quote, err := h.service.GetByID(r.Context(), id)
	writeQuoteResult(w, r, quote, err)
}

func (h *QuoteHandler) getLatest(w http.ResponseWriter, r *http.Request) {
	quote, err := h.service.GetLatest(r.Context(), r.URL.Query().Get("pair"))
	writeQuoteResult(w, r, quote, err)
}

func writeQuoteResult(w http.ResponseWriter, r *http.Request, quote domain.Quote, err error) {
	if err != nil {
		writeQuoteError(w, r, err)
		return
	}

	response := dto.QuoteResponse{ID: quote.ID, Pair: quote.Pair, Status: quote.Status}
	status := http.StatusOK
	switch quote.Status {
	case domain.QuotePending:
		status = http.StatusAccepted
	case domain.QuoteCompleted:
		response.Price = &quote.Price
		response.UpdatedAt = &quote.UpdatedAt
	case domain.QuoteFailed:
		response.Error = "rate_unavailable"
	}
	writeJSON(w, status, response)
}

func writeQuoteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidPair):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, domain.ErrQuoteNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict")
	default:
		slog.Error("serve request", "method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write JSON response", "error", err)
	}
}
