package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"fx-quotes/internal/config"
	"fx-quotes/internal/frankfurter"
	"fx-quotes/internal/handler"
	"fx-quotes/internal/postgres"
	"fx-quotes/internal/service"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("run quote service", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("configure PostgreSQL: %w", err)
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, cfg.DatabasePingTimeout)
	err = pool.Ping(pingCtx)
	cancelPing()
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	quotes := postgres.NewQuoteRepo(pool)
	quoteService := service.NewQuoteService(quotes)
	rates := frankfurter.NewClient(&http.Client{Timeout: cfg.Rates.Timeout}, cfg.Rates)
	worker := service.NewQuoteWorker(quotes, rates, cfg.Worker)

	app := handler.NewQuoteHandler(quoteService, cfg.HTTP).Routes()
	app.Hooks().OnPostShutdown(func(err error) error {
		if err != nil {
			slog.Error("shut down HTTP server", "error", err)
		}
		return nil
	})

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var running sync.WaitGroup
	slog.Info("start quote workers", "count", cfg.Worker.Count)
	running.Go(func() { worker.Run(runCtx) })

	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	slog.Info("start HTTP server", "addr", addr)
	err = app.Listen(addr, fiber.ListenConfig{
		GracefulContext:       runCtx,
		ShutdownTimeout:       cfg.HTTP.ShutdownTimeout,
		DisableStartupMessage: true,
	})
	stop()
	running.Wait()
	return err
}
