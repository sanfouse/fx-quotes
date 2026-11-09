package handler

import (
	"strconv"
	"strings"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/handler/dto"
	"fx-quotes/internal/service"

	"github.com/gofiber/fiber/v3"
)

var errInvalidRequest = fiber.NewError(fiber.StatusBadRequest, "invalid_request")

type QuoteHandler struct {
	service  *service.QuoteService
	settings config.HTTPConfig
}

func NewQuoteHandler(service *service.QuoteService, settings config.HTTPConfig) *QuoteHandler {
	return &QuoteHandler{service: service, settings: settings}
}

func (h *QuoteHandler) enqueue(c fiber.Ctx) error {
	var req dto.EnqueueQuoteRequest
	if err := c.Bind().JSON(&req); err != nil {
		return errInvalidRequest
	}
	key := c.Get("Idempotency-Key")
	if key != "" && (strings.TrimSpace(key) == "" || len(key) > h.settings.MaxIdempotencyKeyBytes) {
		return errInvalidRequest
	}
	id, err := h.service.Enqueue(c.Context(), req.Pair, key)
	if err != nil {
		return err
	}
	c.Location("/quotes/" + strconv.FormatInt(id, 10))
	return c.Status(fiber.StatusAccepted).JSON(dto.QuoteReference{ID: id})
}

func (h *QuoteHandler) getByID(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return errInvalidRequest
	}
	quote, err := h.service.GetByID(c.Context(), id)
	return writeQuoteResult(c, quote, err)
}

func (h *QuoteHandler) getLatest(c fiber.Ctx) error {
	quote, err := h.service.GetLatest(c.Context(), c.Query("pair"))
	return writeQuoteResult(c, quote, err)
}

func writeQuoteResult(c fiber.Ctx, quote domain.Quote, err error) error {
	if err != nil {
		return err
	}

	response := dto.QuoteResponse{ID: quote.ID, Pair: quote.Pair, Status: quote.Status}
	status := fiber.StatusOK
	switch quote.Status {
	case domain.QuotePending:
		status = fiber.StatusAccepted
	case domain.QuoteCompleted:
		response.Price = &quote.Price
		response.UpdatedAt = &quote.UpdatedAt
	case domain.QuoteFailed:
		response.Error = "rate_unavailable"
	}
	return c.Status(status).JSON(response)
}
