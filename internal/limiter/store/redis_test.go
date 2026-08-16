package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"

	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter/store"
)

// RedisSuite cobre só o que a ContratoSuite não alcança: o tempo de vida real
// das chaves, que exige olhar o Redis por fora do contrato. Comportamento comum
// às duas estratégias é testado lá, uma vez, para as duas.
type RedisSuite struct {
	suite.Suite
	store  *store.Redis
	client *redis.Client
	ctx    context.Context
}

func TestRedisSuite(t *testing.T) {
	suite.Run(t, new(RedisSuite))
}

func (s *RedisSuite) SetupSuite() {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		s.T().Skip("REDIS_TEST_ADDR não definido; pulando testes de integração")
	}

	s.ctx = context.Background()

	redisStore, err := store.NewRedis(s.ctx, store.Options{Addr: addr})
	s.Require().NoError(err, "conectar no redis em %s", addr)
	s.store = redisStore

	s.client = redis.NewClient(&redis.Options{Addr: addr})
}

func (s *RedisSuite) TearDownSuite() {
	if s.store != nil {
		s.NoError(s.store.Close())
	}
	if s.client != nil {
		s.NoError(s.client.Close())
	}
}

func (s *RedisSuite) SetupTest() {
	s.Require().NoError(s.client.FlushDB(s.ctx).Err())
}

func (s *RedisSuite) chave() string {
	return "teste:" + s.T().Name()
}

func (s *RedisSuite) countKey() string {
	return "ratelimit:count:" + s.chave()
}

func (s *RedisSuite) ttl(key string) time.Duration {
	s.T().Helper()

	ttl, err := s.client.PTTL(s.ctx, key).Result()
	s.Require().NoErrorf(err, "PTTL de %q", key)

	return ttl
}

func (s *RedisSuite) increment(window time.Duration) {
	s.T().Helper()

	_, err := s.store.Increment(s.ctx, s.chave(), window)
	s.Require().NoError(err)
}

func (s *RedisSuite) TestIncrementReparaChaveSemTempoDeVida() {
	s.increment(time.Minute)

	// Simula o processo que morreu antes de marcar o TTL: a chave existe e é
	// eterna. Sem reparo, a identidade ficaria presa nesta janela para sempre.
	s.Require().NoError(s.client.Persist(s.ctx, s.countKey()).Err())
	s.Require().Negative(s.ttl(s.countKey()), "preparo do teste falhou: a chave ainda tem TTL")

	s.increment(time.Minute)

	s.Positive(s.ttl(s.countKey()), "chave sem tempo de vida não foi reparada")
}

func (s *RedisSuite) TestJanelaMenorQueUmSegundoNaoEhArredondada() {
	// EXPIRE trabalha em segundos e levaria 300ms para 1s em silêncio; o store
	// usa PEXPIRE justamente por isso.
	s.increment(300 * time.Millisecond)

	s.LessOrEqual(s.ttl(s.countKey()), 300*time.Millisecond)
}

func (s *RedisSuite) TestContadorEBloqueioTemTempoDeVidaIndependente() {
	s.increment(time.Minute)
	s.Require().NoError(s.store.Block(s.ctx, s.chave(), time.Hour))

	// TTLs independentes são o que faz o bloqueio sobreviver à virada da janela.
	s.Greater(s.ttl("ratelimit:block:"+s.chave()), s.ttl(s.countKey()))
}
