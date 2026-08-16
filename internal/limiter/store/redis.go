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

// Redis guarda contadores e bloqueios no Redis, o que faz o limite valer para
// todas as instâncias da aplicação, e não só para o processo local.
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

func (r *Redis) Close() error {
	return r.client.Close()
}

// Increment soma 1 no contador e devolve o total da janela atual.
//
// INCR e PTTL vão na mesma transação, então total e tempo de vida descrevem o
// mesmo instante da chave. O TTL só é marcado quando não há um: marcá-lo a cada
// requisição renovaria a janela indefinidamente e, sob tráfego contínuo, o
// contador nunca reiniciaria.
//
// TTL negativo é chave sem expiração — recém-criada, ou órfã de um processo que
// morreu antes de marcá-la. Tratar os dois casos igual é o que faz a requisição
// seguinte reparar sozinha uma chave eterna, que de outro modo prenderia a
// identidade na primeira janela para sempre.
//
// PEXPIRE, e não EXPIRE, porque a janela é configurável e pode ser menor que um
// segundo — EXPIRE arredondaria para cima em silêncio.
func (r *Redis) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	countKey := countPrefix + key

	var (
		total *redis.IntCmd
		ttl   *redis.DurationCmd
	)
	_, err := r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		total = pipe.Incr(ctx, countKey)
		ttl = pipe.PTTL(ctx, countKey)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("incrementar %q: %w", key, err)
	}

	if ttl.Val() < 0 {
		if err := r.client.PExpire(ctx, countKey, window).Err(); err != nil {
			return 0, fmt.Errorf("marcar tempo de vida do contador de %q: %w", key, err)
		}
	}

	return total.Val(), nil
}

// Block grava a marca de bloqueio com tempo de vida igual ao da punição.
func (r *Redis) Block(ctx context.Context, key string, duration time.Duration) error {
	if err := r.client.Set(ctx, blockPrefix+key, 1, duration).Err(); err != nil {
		return fmt.Errorf("bloquear %q: %w", key, err)
	}
	return nil
}

func (r *Redis) Blocked(ctx context.Context, key string) (bool, error) {
	n, err := r.client.Exists(ctx, blockPrefix+key).Result()
	if err != nil {
		return false, fmt.Errorf("consultar bloqueio de %q: %w", key, err)
	}
	return n > 0, nil
}
