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

// RedisSuite roda contra um Redis de verdade. O store em memória prova a regra;
// esta suíte prova o contrato com o Redis — tempo de vida das chaves e reparo —,
// que nenhum fake consegue provar.
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

func (s *RedisSuite) increment(window time.Duration) int64 {
	s.T().Helper()

	total, err := s.store.Increment(s.ctx, s.chave(), window)
	s.Require().NoError(err)

	return total
}

func (s *RedisSuite) TestIncrementContaEReiniciaQuandoAJanelaVence() {
	const window = 300 * time.Millisecond

	for i := int64(1); i <= 3; i++ {
		s.Equalf(i, s.increment(window), "total na requisição %d", i)
	}

	time.Sleep(window + 100*time.Millisecond)

	s.Equal(int64(1), s.increment(window), "contador não reiniciou depois que a janela venceu")
}

func (s *RedisSuite) TestIncrementNaoRenovaAJanelaAcadaRequisicao() {
	const window = time.Second

	s.increment(window)
	time.Sleep(400 * time.Millisecond)
	s.increment(window)

	// Se o segundo acesso empurrasse o vencimento, tráfego contínuo manteria a
	// chave viva e o contador nunca reiniciaria.
	s.Less(s.ttl(s.countKey()), 700*time.Millisecond, "o segundo acesso renovou a janela")
}

func (s *RedisSuite) TestIncrementReparaChaveSemTempoDeVida() {
	const window = time.Minute

	s.increment(window)

	// Simula o processo que morreu antes de marcar o TTL: a chave existe e é
	// eterna. Sem reparo, a identidade ficaria presa nesta janela para sempre.
	s.Require().NoError(s.client.Persist(s.ctx, s.countKey()).Err())
	s.Require().Negative(s.ttl(s.countKey()), "preparo do teste falhou: a chave ainda tem TTL")

	s.increment(window)

	s.Positive(s.ttl(s.countKey()), "chave sem tempo de vida não foi reparada")
}

func (s *RedisSuite) TestIncrementAceitaJanelaMenorQueUmSegundo() {
	// EXPIRE trabalha em segundos e arredondaria 300ms para 1s em silêncio;
	// o store usa PEXPIRE justamente por isso.
	s.increment(300 * time.Millisecond)

	s.LessOrEqual(s.ttl(s.countKey()), 300*time.Millisecond, "a janela sub-segundo foi arredondada para cima")
}

func (s *RedisSuite) TestBlockExpiraNoTempoConfigurado() {
	const duration = 300 * time.Millisecond

	blocked, err := s.store.Blocked(s.ctx, s.chave())
	s.Require().NoError(err)
	s.Require().False(blocked, "chave nova apareceu como bloqueada")

	s.Require().NoError(s.store.Block(s.ctx, s.chave(), duration))

	blocked, err = s.store.Blocked(s.ctx, s.chave())
	s.Require().NoError(err)
	s.True(blocked, "chave bloqueada não apareceu como bloqueada")

	time.Sleep(duration + 100*time.Millisecond)

	blocked, err = s.store.Blocked(s.ctx, s.chave())
	s.Require().NoError(err)
	s.False(blocked, "bloqueio não expirou no tempo configurado")
}

func (s *RedisSuite) TestContadorEBloqueioSaoChavesSeparadas() {
	s.increment(time.Minute)
	s.Require().NoError(s.store.Block(s.ctx, s.chave(), time.Hour))

	// TTLs independentes são o que faz o bloqueio sobreviver à virada da janela.
	s.Positive(s.ttl(s.countKey()))
	s.Greater(s.ttl("ratelimit:block:"+s.chave()), s.ttl(s.countKey()))
}
