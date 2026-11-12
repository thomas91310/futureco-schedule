// Package main serves the futureco-schedule HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"futureco-schedule/internal/httpapi"
	"futureco-schedule/internal/observability"
	"futureco-schedule/internal/schedule"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	_ "futureco-schedule/docs"
)

// @title           futureco-schedule
// @version         1.0
// @description     Trainer scheduling API. Schedule routes use the /api/v1 prefix below. GET /api/health checks the database and is not versioned, because the later e2e warm-up calls that path.
// @host            localhost:8080
// @BasePath        /
// @schemes         http https
func main() {
	if err := run(); err != nil {
		slog.Error("serving http", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	observability.SetupLogger()

	shutdownTracing, err := observability.SetupTracing(ctx)
	if err != nil {
		return fmt.Errorf("setting up tracing: %w", err)
	}

	defer shutdownTracing()

	observability.SetupProfiling()

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}

	defer pool.Close()

	observability.RegisterPoolMetrics(pool)

	server := &http.Server{
		Addr:              listenAddr(),
		Handler:           otelhttp.NewHandler(newMux(pool), "futureco-schedule"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Error("shutting down http server", "error", shutdownErr)
		}
	}()

	slog.Info("listening", "addr", server.Addr)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving http: %w", err)
	}

	return nil
}

func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	return ":" + port
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DATABASE_URL: %w", err)
	}
	// MaxConns is 3 because a later Vercel deploy runs many short-lived
	// isolates, and each isolate opens its own pool. A larger pool per
	// isolate would exhaust Neon through the pooler. The database skill's
	// example of 25 connections is for one long-running server, not for
	// many isolates sharing one Postgres.
	cfg.MaxConns = 3
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("opening database pool: %w", err)
	}

	return pool, nil
}

func newMux(pool *pgxpool.Pool) http.Handler {
	handler := &httpapi.Handler{Store: schedule.NewStore(pool)}
	mux := http.NewServeMux()
	mux.Handle("GET /api/health", observability.Instrument("GET /api/health", http.HandlerFunc(handler.Health)))
	mux.Handle("GET /api/v1/trainers/{trainer_id}/available-times", observability.Instrument(
		"GET /api/v1/trainers/{trainer_id}/available-times",
		http.HandlerFunc(handler.AvailableTimes),
	))
	mux.Handle("POST /api/v1/appointments", observability.Instrument(
		"POST /api/v1/appointments",
		http.HandlerFunc(handler.CreateAppointment),
	))
	mux.Handle("GET /api/v1/trainers/{trainer_id}/appointments", observability.Instrument(
		"GET /api/v1/trainers/{trainer_id}/appointments",
		http.HandlerFunc(handler.ListAppointments),
	))
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	return httpapi.JSONErrors(mux)
}
