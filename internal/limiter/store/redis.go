package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	countPrefix = "ratelimit:count:"
	blockPrefix = "ratelimit:block:"
)

// Redis guarda contadores e bloqueios no Redis, o que faz o limite valer
// para todas as instâncias da aplicação, e não só para o processo local.
type Redis struct {
	client *redis.Client
}

// Options descreve a conexão com o Redis.
type Options struct {
	Addr     string
	Password string
	DB       int
}

// NewRedis abre o client e confirma que o servidor responde.
func NewRedis(ctx context.Context, opts Options) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("conectar no redis em %s: %w", opts.Addr, err)
	}
	return &Redis{client: client}, nil
}

// Close encerra a conexão.
func (r *Redis) Close() error {
	return r.client.Close()
}

// incrementScript soma 1 no contador e aplica o TTL da janela apenas quando a
// chave nasce. Roda como script para ser um passo atômico: um processo que
// morresse entre o INCR e o PEXPIRE deixaria a chave sem expiração e o IP
// preso na primeira janela para sempre. PEXPIRE (e não EXPIRE) porque a janela
// é configurável e pode ser menor que um segundo.
var incrementScript = redis.NewScript(`
	local total = redis.call('INCR', KEYS[1])
	if total == 1 then
		redis.call('PEXPIRE', KEYS[1], ARGV[1])
	end
	return total
`)

// Increment soma 1 no contador e devolve o total da janela atual.
func (r *Redis) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	total, err := incrementScript.Run(ctx, r.client, []string{countPrefix + key}, window.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("incrementar %q: %w", key, err)
	}

	return total, nil
}

// Block grava a marca de bloqueio com TTL igual ao tempo de punição.
func (r *Redis) Block(ctx context.Context, key string, duration time.Duration) error {
	if err := r.client.Set(ctx, blockPrefix+key, 1, duration).Err(); err != nil {
		return fmt.Errorf("bloquear %q: %w", key, err)
	}
	return nil
}

// Blocked informa se a marca de bloqueio da chave ainda existe.
func (r *Redis) Blocked(ctx context.Context, key string) (bool, error) {
	n, err := r.client.Exists(ctx, blockPrefix+key).Result()
	if err != nil {
		return false, fmt.Errorf("consultar bloqueio de %q: %w", key, err)
	}
	return n > 0, nil
}
