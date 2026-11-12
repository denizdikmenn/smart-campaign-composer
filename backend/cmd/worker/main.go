// Command worker sends queued campaign messages through the mock SMS and
// email providers. It shares the API's PostgreSQL database and has no HTTP
// port; `worker -healthcheck` reports whether a worker heartbeat is recent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/content"
	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/delivery"
	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

func main() {
	health := flag.Bool("healthcheck", false, "exit 0 if a worker heartbeat is recent")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var err error
	if *health {
		err = healthcheck()
	} else {
		err = run(log)
	}
	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	setupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := db.Migrate(setupCtx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	cfg := delivery.DefaultConfig
	if cfg.Rate, err = intEnv("DELIVERY_RATE_PER_SECOND", cfg.Rate); err != nil {
		return err
	}
	if cfg.Concurrency, err = intEnv("DELIVERY_CONCURRENCY", cfg.Concurrency); err != nil {
		return err
	}
	seed, err := intEnv("MOCK_FAULT_SEED", int(time.Now().UnixNano()&0xffff))
	if err != nil {
		return err
	}
	rates, err := delivery.ParseFaultRates(os.Getenv("MOCK_FAULT_RATES"), delivery.DefaultFaultRates)
	if err != nil {
		return err
	}
	minLat, maxLat, err := latencyEnv("MOCK_LATENCY_MS", 80*time.Millisecond, 400*time.Millisecond)
	if err != nil {
		return err
	}
	faults, recipients := delivery.RandomFaults(rates, uint64(seed)), delivery.SyntheticRecipients
	if os.Getenv("MOCK_PROVIDER_FAULTS") == "off" {
		faults, recipients = nil, nil
	}
	providers := map[string]delivery.Provider{}
	for _, ch := range []string{"sms", "email"} {
		providers[ch] = &delivery.Mock{Pool: pool, Channel: ch, MinLatency: minLat, MaxLatency: maxLat,
			RetryAfter: 2 * time.Second, Faults: faults, Recipients: recipients}
	}
	log.Info("delivery worker started", "rate_per_channel", cfg.Rate, "concurrency", cfg.Concurrency, "faults", faults != nil, "fault_rates", rates, "fault_seed", seed, "latency", fmt.Sprintf("%v-%v", minLat, maxLat))
	// Scheduled campaigns are resolved at send time like "send now"; the
	// message resolver never calls the generators, so mocks are enough here.
	seg := segmentation.NewService(pool, segmentation.Mock{}, log)
	src := delivery.ContentSource(content.NewService(pool, content.Mock{}, seg, log))
	delivery.NewWorker(pool, providers, cfg, log).WithSource(src).Run(ctx)
	log.Info("delivery worker stopped")
	return nil
}

func healthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	var fresh bool
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(seen_at) > now() - interval '30 seconds', false) FROM worker_heartbeats`).Scan(&fresh); err != nil {
		return err
	}
	if !fresh {
		return errors.New("no recent worker heartbeat")
	}
	return nil
}

func connect(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	return pgxpool.NewWithConfig(ctx, cfg)
}

func intEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}

// latencyEnv reads "min-max" in milliseconds.
func latencyEnv(key string, defMin, defMax time.Duration) (time.Duration, time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defMin, defMax, nil
	}
	a, b, ok := strings.Cut(v, "-")
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if !ok || err1 != nil || err2 != nil || lo < 0 || hi < lo {
		return 0, 0, fmt.Errorf("%s must be min-max in milliseconds, e.g. 80-400", key)
	}
	return time.Duration(lo) * time.Millisecond, time.Duration(hi) * time.Millisecond, nil
}
