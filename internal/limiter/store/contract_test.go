package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter"
	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter/store"
)

// ContratoSuite roda as mesmas asserções contra toda implementação de
// limiter.Store. É o que sustenta a premissa da Strategy: se as estratégias não
// se comportam igual, trocar uma pela outra muda o comportamento do sistema.
//
// Usa tempo real, e não relógio injetado, porque o contrato precisa valer
// também para quem não tem relógio para injetar — o Redis.
type ContratoSuite struct {
	suite.Suite
	novo func() limiter.Store
	loja limiter.Store
	ctx  context.Context
}

const janelaCurta = 200 * time.Millisecond

func TestContratoMemory(t *testing.T) {
	suite.Run(t, &ContratoSuite{novo: func() limiter.Store { return store.NewMemory() }})
}

func TestContratoRedis(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR não definido; pulando contrato contra o Redis")
	}

	suite.Run(t, &ContratoSuite{novo: func() limiter.Store {
		redisStore, err := store.NewRedis(context.Background(), store.Options{Addr: addr})
		if err != nil {
			t.Fatalf("conectar no redis: %v", err)
		}
		return redisStore
	}})
}

func (s *ContratoSuite) SetupTest() {
	s.ctx = context.Background()
	s.loja = s.novo()
}

// chave isola cada teste, inclusive entre execuções contra o mesmo Redis.
func (s *ContratoSuite) chave() string {
	return "contrato:" + s.T().Name()
}

func (s *ContratoSuite) incr(key string, window time.Duration) int64 {
	s.T().Helper()

	total, err := s.loja.Increment(s.ctx, key, window)
	s.Require().NoError(err)

	return total
}

func (s *ContratoSuite) bloqueado(key string) bool {
	s.T().Helper()

	blocked, err := s.loja.Blocked(s.ctx, key)
	s.Require().NoError(err)

	return blocked
}

func (s *ContratoSuite) TestContagemComecaEmUmESobeDeUmEmUm() {
	for esperado := int64(1); esperado <= 3; esperado++ {
		s.Equal(esperado, s.incr(s.chave(), time.Minute))
	}
}

func (s *ContratoSuite) TestChavesDiferentesTemContadoresIndependentes() {
	s.incr(s.chave()+":a", time.Minute)
	s.incr(s.chave()+":a", time.Minute)

	s.Equal(int64(1), s.incr(s.chave()+":b", time.Minute))
}

func (s *ContratoSuite) TestContadorReiniciaDepoisQueAJanelaVence() {
	s.incr(s.chave(), janelaCurta)
	s.incr(s.chave(), janelaCurta)

	time.Sleep(janelaCurta + 100*time.Millisecond)

	s.Equal(int64(1), s.incr(s.chave(), janelaCurta))
}

func (s *ContratoSuite) TestJanelaNaoEhRenovadaAcadaIncremento() {
	s.incr(s.chave(), janelaCurta)
	time.Sleep(janelaCurta / 2)
	s.incr(s.chave(), janelaCurta)
	time.Sleep(janelaCurta/2 + 50*time.Millisecond)

	// A janela conta do primeiro acesso: se o segundo a empurrasse, tráfego
	// contínuo manteria o contador vivo para sempre.
	s.Equal(int64(1), s.incr(s.chave(), janelaCurta))
}

func (s *ContratoSuite) TestChaveNovaNaoEstaBloqueada() {
	s.False(s.bloqueado(s.chave()))
}

func (s *ContratoSuite) TestBlockBloqueiaEExpiraSozinho() {
	s.Require().NoError(s.loja.Block(s.ctx, s.chave(), janelaCurta))
	s.True(s.bloqueado(s.chave()))

	time.Sleep(janelaCurta + 100*time.Millisecond)

	s.False(s.bloqueado(s.chave()), "o bloqueio não expirou sozinho")
}

func (s *ContratoSuite) TestBloqueioNaoInterfereNoContador() {
	s.incr(s.chave(), time.Minute)
	s.Require().NoError(s.loja.Block(s.ctx, s.chave(), time.Minute))

	s.Equal(int64(2), s.incr(s.chave(), time.Minute), "o bloqueio zerou ou duplicou o contador")
}

func (s *ContratoSuite) TestBloquearUmaChaveNaoBloqueiaOutra() {
	s.Require().NoError(s.loja.Block(s.ctx, s.chave()+":a", time.Minute))

	s.False(s.bloqueado(s.chave() + ":b"))
}
