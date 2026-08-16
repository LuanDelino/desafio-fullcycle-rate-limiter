package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter/store"
)

// newRedis abre um store contra um Redis de verdade. O teste em memória prova
// a regra; este prova o contrato com o Redis (TTL da janela, TTL do bloqueio),
// que nenhum fake consegue provar.
func newRedis(t *testing.T) *store.Redis {
	t.Helper()

	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR não definido; pulando teste de integração")
	}

	ctx := context.Background()
	redisStore, err := store.NewRedis(ctx, store.Options{Addr: addr})
	if err != nil {
		t.Fatalf("conectar no redis: %v", err)
	}
	t.Cleanup(func() { _ = redisStore.Close() })

	return redisStore
}

func TestRedisIncrementCriaJanelaEExpiraSozinho(t *testing.T) {
	redisStore := newRedis(t)
	ctx := context.Background()
	key := "teste-janela-" + t.Name()

	for i := int64(1); i <= 3; i++ {
		got, err := redisStore.Increment(ctx, key, 300*time.Millisecond)
		if err != nil {
			t.Fatalf("Increment: %v", err)
		}
		if got != i {
			t.Fatalf("contador = %d, esperado %d", got, i)
		}
	}

	// Se o ExpireNX não estivesse na transação, ou renovasse o TTL a cada
	// INCR, a chave nunca venceria e o contador jamais reiniciaria.
	time.Sleep(400 * time.Millisecond)

	got, err := redisStore.Increment(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Increment depois da janela: %v", err)
	}
	if got != 1 {
		t.Fatalf("contador depois da janela = %d, esperado 1", got)
	}
}

func TestRedisBlockExpiraNoTempoConfigurado(t *testing.T) {
	redisStore := newRedis(t)
	ctx := context.Background()
	key := "teste-bloqueio-" + t.Name()

	blocked, err := redisStore.Blocked(ctx, key)
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if blocked {
		t.Fatal("chave nova apareceu como bloqueada")
	}

	if err := redisStore.Block(ctx, key, 300*time.Millisecond); err != nil {
		t.Fatalf("Block: %v", err)
	}

	blocked, err = redisStore.Blocked(ctx, key)
	if err != nil {
		t.Fatalf("Blocked depois de Block: %v", err)
	}
	if !blocked {
		t.Fatal("chave bloqueada não apareceu como bloqueada")
	}

	time.Sleep(400 * time.Millisecond)

	blocked, err = redisStore.Blocked(ctx, key)
	if err != nil {
		t.Fatalf("Blocked depois do TTL: %v", err)
	}
	if blocked {
		t.Fatal("bloqueio não expirou no tempo configurado")
	}
}
