package main

import (
	"context"
	"errors"
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

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:           handler.NewQuoteHandler(quoteService, cfg.HTTP).Routes(),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	runCtx, stop := context.WithCancel(ctx)
	var running sync.WaitGroup
	running.Go(func() { worker.Run(runCtx) })
	running.Go(func() {
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shut down HTTP server", "error", err)
			_ = server.Close()
		}
	})

	slog.Info("start HTTP server", "addr", server.Addr)
	err = server.ListenAndServe()
	stop()
	running.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
