package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

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

func newClient(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR não definido; pulando teste de integração")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	return client
}

func countKeyDe(key string) string { return "ratelimit:count:" + key }

// ttlDe lê o tempo de vida restante de uma chave; negativo significa eterna.
func ttlDe(t *testing.T, key string) time.Duration {
	t.Helper()

	ttl, err := newClient(t).PTTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("PTTL de %q: %v", key, err)
	}
	return ttl
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

	time.Sleep(400 * time.Millisecond)

	got, err := redisStore.Increment(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Increment depois da janela: %v", err)
	}
	if got != 1 {
		t.Fatalf("contador depois da janela = %d, esperado 1", got)
	}
}

func TestRedisIncrementNaoRenovaAJanelaAcadaRequisicao(t *testing.T) {
	redisStore := newRedis(t)
	ctx := context.Background()
	key := "teste-sem-renovar-" + t.Name()

	if _, err := redisStore.Increment(ctx, key, time.Second); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := redisStore.Increment(ctx, key, time.Second); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	// O segundo acesso não pode empurrar o vencimento para frente: se
	// empurrasse, tráfego contínuo manteria a chave viva e o contador nunca
	// reiniciaria.
	ttl := ttlDe(t, countKeyDe(key))
	if ttl > 700*time.Millisecond {
		t.Fatalf("TTL restante = %v; o segundo acesso renovou a janela", ttl)
	}
}

func TestRedisIncrementReparaChaveSemTempoDeVida(t *testing.T) {
	redisStore := newRedis(t)
	ctx := context.Background()
	key := "teste-reparo-" + t.Name()

	if _, err := redisStore.Increment(ctx, key, time.Minute); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	// Simula o processo que morreu antes de marcar o TTL: a chave existe e é
	// eterna. Sem reparo, a identidade ficaria presa nesta janela para sempre.
	client := newClient(t)
	if err := client.Persist(ctx, countKeyDe(key)).Err(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if ttl := ttlDe(t, countKeyDe(key)); ttl >= 0 {
		t.Fatalf("preparo do teste falhou: chave ainda tem TTL de %v", ttl)
	}

	if _, err := redisStore.Increment(ctx, key, time.Minute); err != nil {
		t.Fatalf("Increment na chave eterna: %v", err)
	}

	if ttl := ttlDe(t, countKeyDe(key)); ttl <= 0 {
		t.Fatalf("chave sem tempo de vida não foi reparada; TTL = %v", ttl)
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
