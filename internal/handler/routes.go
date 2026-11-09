package handler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"fx-quotes/internal/domain"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func (h *QuoteHandler) Routes() *fiber.App {
	app := fiber.New(fiber.Config{
		BodyLimit:               h.settings.MaxBodyBytes,
		ReadTimeout:             h.settings.ReadTimeout,
		WriteTimeout:            h.settings.WriteTimeout,
		IdleTimeout:             h.settings.IdleTimeout,
		CaseSensitive:           true,
		StrictRouting:           true,
		DisableHeadAutoRegister: true,
		ErrorHandler:            writeError,
	})
	app.Use(func(c fiber.Ctx) error {
		started := time.Now()
		ctx, cancel := context.WithTimeout(c.Context(), h.settings.RequestTimeout)
		defer cancel()
		c.SetContext(ctx)

		err := c.Next()
		slog.InfoContext(ctx, "http request", "method", c.Method(), "path", c.Path(),
			"duration", time.Since(started))
		return err
	})
	app.Use(recover.New())
	app.Post("/quotes", h.enqueue)
	app.Get("/quotes/latest", h.getLatest)
	app.Get("/quotes/:id", h.getByID)
	return app
}

func writeError(c fiber.Ctx, err error) error {
	status, message := fiber.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, domain.ErrInvalidPair):
		status, message = fiber.StatusBadRequest, "invalid_request"
	case errors.Is(err, domain.ErrQuoteNotFound):
		status, message = fiber.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrIdempotencyConflict):
		status, message = fiber.StatusConflict, "idempotency_conflict"
	default:
		if e, ok := errors.AsType[*fiber.Error](err); ok {
			status, message = e.Code, e.Message
		} else {
			slog.ErrorContext(c.Context(), "serve request", "method", c.Method(), "path", c.Path(), "error", err)
		}
	}
	return c.Status(status).JSON(fiber.Map{"error": message})
}
