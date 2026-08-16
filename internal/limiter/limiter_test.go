package limiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter"
	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter/store"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type LimiterSuite struct {
	suite.Suite
	clock *clock
}

func TestLimiterSuite(t *testing.T) {
	suite.Run(t, new(LimiterSuite))
}

func (s *LimiterSuite) SetupTest() {
	s.clock = &clock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}
}

func (s *LimiterSuite) limiterCom(cfg limiter.Config) *limiter.Limiter {
	return limiter.New(store.NewMemoryWithClock(s.clock.Now), cfg)
}

// allow falha o teste na hora se o store devolver erro: nenhuma asserção sobre
// o veredito faz sentido depois disso.
func (s *LimiterSuite) allow(l *limiter.Limiter, ip, token string) limiter.Result {
	s.T().Helper()

	result, err := l.Allow(context.Background(), ip, token)
	s.Require().NoErrorf(err, "Allow(%q, %q)", ip, token)

	return result
}

func (s *LimiterSuite) TestPermiteAteOLimiteDoIPEBloqueiaODepois() {
	l := s.limiterCom(limiter.Config{IPLimit: 3, Window: time.Second, BlockDuration: 5 * time.Minute})

	for i := 1; i <= 3; i++ {
		s.Truef(s.allow(l, "10.0.0.1", "").Allowed, "requisição %d dentro do limite foi rejeitada", i)
	}

	s.False(s.allow(l, "10.0.0.1", "").Allowed, "requisição acima do limite do IP foi permitida")
}

func (s *LimiterSuite) TestIPsDiferentesTemContadoresIndependentes() {
	l := s.limiterCom(limiter.Config{IPLimit: 1, Window: time.Second, BlockDuration: time.Minute})

	s.Require().True(s.allow(l, "10.0.0.1", "").Allowed)

	s.True(s.allow(l, "10.0.0.2", "").Allowed, "o estouro de um IP afetou o contador de outro IP")
}

func (s *LimiterSuite) TestLimiteDoTokenSeSobrepoeAoDoIP() {
	l := s.limiterCom(limiter.Config{
		IPLimit:       2,
		TokenLimits:   map[string]int64{"abc123": 5},
		Window:        time.Second,
		BlockDuration: 5 * time.Minute,
	})

	for i := 1; i <= 5; i++ {
		s.Truef(s.allow(l, "10.0.0.1", "abc123").Allowed, "requisição %d do token foi rejeitada antes do limite dele", i)
	}

	s.False(s.allow(l, "10.0.0.1", "abc123").Allowed, "requisição acima do limite do token foi permitida")
}

func (s *LimiterSuite) TestTokenMaisRestritivoQueOIPValeMesmoAssim() {
	l := s.limiterCom(limiter.Config{
		IPLimit:       100,
		TokenLimits:   map[string]int64{"restrito": 1},
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	s.Require().True(s.allow(l, "10.0.0.1", "restrito").Allowed)

	// Precedência vale nos dois sentidos: o token também aperta, não só afrouxa.
	s.False(s.allow(l, "10.0.0.1", "restrito").Allowed, "token mais restritivo que o IP não foi respeitado")
}

func (s *LimiterSuite) TestTokenNaoCadastradoCaiNoLimiteDoIP() {
	l := s.limiterCom(limiter.Config{
		IPLimit:       1,
		TokenLimits:   map[string]int64{"abc123": 100},
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	s.Require().True(s.allow(l, "10.0.0.1", "token-inventado").Allowed)

	// Se um token desconhecido ganhasse limite próprio, bastaria inventar um
	// header para contornar o limite por IP.
	s.False(s.allow(l, "10.0.0.1", "token-inventado").Allowed, "token não cadastrado escapou do limite do IP")
}

func (s *LimiterSuite) TestIdentidadeEscolhidaApareceNoResultado() {
	l := s.limiterCom(limiter.Config{
		IPLimit:     10,
		TokenLimits: map[string]int64{"abc123": 100},
		Window:      time.Second,
	})

	casos := []struct {
		nome      string
		ip        string
		token     string
		esperaKey string
		esperaLim int64
	}{
		{"sem token usa o IP", "10.0.0.1", "", "ip:10.0.0.1", 10},
		{"token cadastrado usa o token", "10.0.0.1", "abc123", "token:abc123", 100},
		{"token desconhecido usa o IP", "10.0.0.1", "nao-existe", "ip:10.0.0.1", 10},
	}

	for _, caso := range casos {
		s.Run(caso.nome, func() {
			result := s.allow(l, caso.ip, caso.token)

			s.Equal(caso.esperaKey, result.Key)
			s.Equal(caso.esperaLim, result.Limit)
		})
	}
}

func (s *LimiterSuite) TestContadorReiniciaNaJanelaSeguinte() {
	l := s.limiterCom(limiter.Config{IPLimit: 2, Window: time.Second, BlockDuration: time.Minute})

	s.Require().True(s.allow(l, "10.0.0.1", "").Allowed)
	s.Require().True(s.allow(l, "10.0.0.1", "").Allowed)

	s.clock.Advance(time.Second)

	s.True(s.allow(l, "10.0.0.1", "").Allowed, "contador não reiniciou na janela seguinte")
}

func (s *LimiterSuite) TestBloqueioSobreviveAoFimDaJanela() {
	l := s.limiterCom(limiter.Config{IPLimit: 1, Window: time.Second, BlockDuration: 5 * time.Minute})

	s.Require().True(s.allow(l, "10.0.0.1", "").Allowed)
	s.Require().False(s.allow(l, "10.0.0.1", "").Allowed, "a segunda requisição deveria estourar o limite")

	// A janela virou, mas a punição é maior que ela: continua bloqueado.
	s.clock.Advance(2 * time.Second)
	s.False(s.allow(l, "10.0.0.1", "").Allowed, "nova janela liberou um IP ainda dentro do tempo de bloqueio")

	s.clock.Advance(5 * time.Minute)
	s.True(s.allow(l, "10.0.0.1", "").Allowed, "IP continuou bloqueado depois do fim do tempo de bloqueio")
}

func (s *LimiterSuite) TestRemainingDecrescePorRequisicao() {
	l := s.limiterCom(limiter.Config{IPLimit: 3, Window: time.Second, BlockDuration: time.Minute})

	for i, esperado := range []int64{2, 1, 0} {
		s.Equalf(esperado, s.allow(l, "10.0.0.1", "").Remaining, "Remaining depois da requisição %d", i+1)
	}
}

// storeQuebrado devolve erro em toda consulta, para provar que a falha de
// persistência sobe como erro em vez de virar liberação silenciosa.
type storeQuebrado struct{}

func (storeQuebrado) Increment(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("store fora do ar")
}
func (storeQuebrado) Block(context.Context, string, time.Duration) error {
	return errors.New("store fora do ar")
}
func (storeQuebrado) Blocked(context.Context, string) (bool, error) {
	return false, errors.New("store fora do ar")
}

func (s *LimiterSuite) TestFalhaDoStoreViraErroENaoLiberacao() {
	l := limiter.New(storeQuebrado{}, limiter.Config{IPLimit: 1, Window: time.Second})

	result, err := l.Allow(context.Background(), "10.0.0.1", "")

	s.Require().Error(err, "falha do store não devolveu erro")
	s.ErrorContains(err, "store fora do ar", "o erro do store deve chegar embrulhado, não trocado")
	s.False(result.Allowed, "falha do store liberou a requisição")
}
