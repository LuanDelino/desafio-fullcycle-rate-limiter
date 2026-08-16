// Command server sobe o servidor HTTP protegido pelo rate limiter.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/luanperes/fullcycle-rate-limiter/internal/config"
	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter"
	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter/store"
	"github.com/luanperes/fullcycle-rate-limiter/internal/middleware"
)

func main() {
	if err := run(); err != nil {
		slog.Error("servidor encerrado com erro", "erro", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	limiterStore, closeStore, err := buildStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	rateLimiter := limiter.New(limiterStore, limiter.Config{
		IPLimit:       cfg.IPLimit,
		TokenLimits:   cfg.TokenLimits,
		Window:        cfg.Window,
		BlockDuration: cfg.BlockDuration,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", hello)
	mux.HandleFunc("GET /health", health)

	server := &http.Server{
		Addr: ":" + cfg.ServerPort,
		// O limiter é o filtro mais externo: ele decide antes de qualquer
		// autenticação ou trabalho de handler.
		Handler:           middleware.RateLimit(rateLimiter)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("servidor no ar",
			"porta", cfg.ServerPort,
			"store", cfg.Store,
			"limite_ip", cfg.IPLimit,
			"janela", cfg.Window,
			"bloqueio", cfg.BlockDuration,
			"tokens_configurados", len(cfg.TokenLimits),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("falha ao servir", "erro", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("encerrando servidor")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// buildStore escolhe a estratégia de persistência conforme a configuração.
// Trocar de estratégia é mudar RATE_LIMIT_STORE — nenhum outro código muda.
func buildStore(ctx context.Context, cfg config.Config) (limiter.Store, func(), error) {
	switch cfg.Store {
	case "memory":
		return store.NewMemory(), func() {}, nil
	default:
		redisStore, err := store.NewRedis(ctx, store.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})
		if err != nil {
			return nil, nil, err
		}
		return redisStore, func() { _ = redisStore.Close() }, nil
	}
}

func hello(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
