package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/customers"
	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/seed"
)

const (
	dbConnectTimeout = 5 * time.Second
	dbPingTimeout    = 2 * time.Second
	dbSetupTimeout   = 60 * time.Second
	shutdownTimeout  = 10 * time.Second
)

// pinger is satisfied by *pgxpool.Pool.
type pinger interface {
	Ping(ctx context.Context) error
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := ":" + getenv("PORT", "8080")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	cfg.ConnConfig.ConnectTimeout = dbConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := setupDatabase(ctx, log, pool); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           newHandler(log, pool, customers.NewStore(pool)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// setupDatabase applies the schema and, when SEED_CUSTOMERS > 0 and no
// customers exist yet, loads that many synthetic customers.
func setupDatabase(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool) error {
	seedCount, err := strconv.Atoi(getenv("SEED_CUSTOMERS", "0"))
	if err != nil || seedCount < 0 {
		return errors.New("SEED_CUSTOMERS must be a non-negative integer")
	}

	ctx, cancel := context.WithTimeout(ctx, dbSetupTimeout)
	defer cancel()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if seedCount == 0 {
		return nil
	}
	seeded, err := seed.SeedIfEmpty(ctx, pool, seedCount, time.Now())
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	log.Info("database ready", "seeded", seeded, "customers", seedCount)
	return nil
}

func newHandler(log *slog.Logger, db pinger, cs customerStore) http.Handler {
	mux := http.NewServeMux()
	ch := customerHandler{log: log, store: cs}
	mux.HandleFunc("GET /api/customers/summary", ch.summary)
	mux.HandleFunc("GET /api/customers", ch.list)
	mux.HandleFunc("GET /api/audiences", ch.audiences)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), dbPingTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "database": "down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "database": "up"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
