// Package limiter contém a regra do rate limiter, sem dependência de HTTP ou de
// um mecanismo de persistência concreto.
package limiter

import (
	"context"
	"fmt"
	"time"
)

// Store é a estratégia de persistência do limiter. Trocar Redis por outro
// mecanismo é implementar esta interface — nada mais no pacote muda.
type Store interface {
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)
	Block(ctx context.Context, key string, duration time.Duration) error
	Blocked(ctx context.Context, key string) (bool, error)
}

type Config struct {
	IPLimit int64
	// TokenLimits mapeia token para o máximo por janela. Token ausente daqui é
	// tratado como se não houvesse token.
	TokenLimits   map[string]int64
	BlockDuration time.Duration
	Window        time.Duration
}

type Limiter struct {
	store Store
	cfg   Config
}

func New(store Store, cfg Config) *Limiter {
	if cfg.Window <= 0 {
		cfg.Window = time.Second
	}
	return &Limiter{store: store, cfg: cfg}
}

type Result struct {
	Allowed bool
	// Key é a identidade que decidiu o veredito: token:... ou ip:...
	Key       string
	Limit     int64
	Remaining int64
}

// Allow aplica a precedência do token sobre o IP e decide a requisição.
func (l *Limiter) Allow(ctx context.Context, ip, token string) (Result, error) {
	key, limit := l.resolve(ip, token)

	blocked, err := l.store.Blocked(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("consultar bloqueio de %q: %w", key, err)
	}
	if blocked {
		return Result{Allowed: false, Key: key, Limit: limit}, nil
	}

	count, err := l.store.Increment(ctx, key, l.cfg.Window)
	if err != nil {
		return Result{}, fmt.Errorf("incrementar contador de %q: %w", key, err)
	}

	if count > limit {
		if err := l.store.Block(ctx, key, l.cfg.BlockDuration); err != nil {
			return Result{}, fmt.Errorf("bloquear %q: %w", key, err)
		}
		return Result{Allowed: false, Key: key, Limit: limit}, nil
	}

	return Result{Allowed: true, Key: key, Limit: limit, Remaining: limit - count}, nil
}

// resolve escolhe a identidade e o teto da requisição. Token não cadastrado cai
// no limite do IP: dar limite próprio a token desconhecido tornaria o limite por
// IP contornável por header inventado.
func (l *Limiter) resolve(ip, token string) (key string, limit int64) {
	if token != "" {
		if tokenLimit, ok := l.cfg.TokenLimits[token]; ok {
			return "token:" + token, tokenLimit
		}
	}
	return "ip:" + ip, l.cfg.IPLimit
}
