package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"

	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter"
	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter/store"
)

// AceitacaoSuite prova o que o docs/desafio.md pede, pela mesma montagem de
// middlewares que o main usa. As outras suítes provam as peças.
type AceitacaoSuite struct {
	suite.Suite
	servidor *httptest.Server
	client   *redis.Client
	ctx      context.Context
}

const (
	// Janela folgada: com 1s, uma execução que cruzasse a virada do segundo
	// reiniciaria o contador no meio do teste. O bloqueio é maior que a janela,
	// como em produção.
	janelaDeTeste  = 3 * time.Second
	bloqueioDeTest = 10 * time.Second

	limitePorIP    = 10
	tokenGeneroso  = "token-generoso"
	limiteGeneroso = 25
	tokenRestrito  = "token-restrito"
	limiteRestrito = 2

	mensagemDoDesafio = "you have reached the maximum number of requests or actions allowed within a certain time frame"
)

func TestAceitacaoSuite(t *testing.T) {
	suite.Run(t, new(AceitacaoSuite))
}

func (s *AceitacaoSuite) SetupSuite() {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		s.T().Skip("REDIS_TEST_ADDR não definido; pulando testes de aceitação")
	}

	s.ctx = context.Background()
	s.client = redis.NewClient(&redis.Options{Addr: addr})

	redisStore, err := store.NewRedis(s.ctx, store.Options{Addr: addr})
	s.Require().NoError(err, "o sistema não sobe sem acesso ao Redis")

	rateLimiter := limiter.New(redisStore, limiter.Config{
		IPLimit: limitePorIP,
		TokenLimits: map[string]int64{
			tokenGeneroso: limiteGeneroso,
			tokenRestrito: limiteRestrito,
		},
		Window:        janelaDeTeste,
		BlockDuration: bloqueioDeTest,
	})

	s.servidor = httptest.NewServer(newHandler(rateLimiter))
}

func (s *AceitacaoSuite) TearDownSuite() {
	if s.servidor != nil {
		s.servidor.Close()
	}
	if s.client != nil {
		s.NoError(s.client.Close())
	}
}

func (s *AceitacaoSuite) SetupTest() {
	s.Require().NoError(s.client.FlushDB(s.ctx).Err())
}

func (s *AceitacaoSuite) chamar(token string) (int, string) {
	s.T().Helper()

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.servidor.URL+"/", nil)
	s.Require().NoError(err)
	if token != "" {
		req.Header.Set("API_KEY", token)
	}

	resp, err := s.servidor.Client().Do(req)
	s.Require().NoError(err)
	defer func() { s.NoError(resp.Body.Close()) }()

	corpo, err := io.ReadAll(resp.Body)
	s.Require().NoError(err)

	return resp.StatusCode, string(corpo)
}

func (s *AceitacaoSuite) chamarVezes(n int, token string) (aceitas int) {
	s.T().Helper()

	for i := 0; i < n; i++ {
		if status, _ := s.chamar(token); status == http.StatusOK {
			aceitas++
		}
	}
	return aceitas
}

func (s *AceitacaoSuite) chaves(padrao string) []string {
	s.T().Helper()

	encontradas, err := s.client.Keys(s.ctx, padrao).Result()
	s.Require().NoError(err)

	return encontradas
}

func (s *AceitacaoSuite) TestSistemaAtendeComORedisAcessivel() {
	s.Require().NoError(s.client.Ping(s.ctx).Err(), "o Redis do teste não respondeu")

	status, corpo := s.chamar("")

	s.Equal(http.StatusOK, status)
	s.Equal("ok", corpo)
}

func (s *AceitacaoSuite) TestPassamExatamenteAsRequisicoesDoLimiteDoIP() {
	s.Equal(limitePorIP, s.chamarVezes(limitePorIP+5, ""), "o número de requisições aceitas não bateu com o limite do IP")
}

func (s *AceitacaoSuite) TestRequisicaoAcimaDoLimiteResponde429() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP, ""))

	status, _ := s.chamar("")

	s.Equal(http.StatusTooManyRequests, status)
}

func (s *AceitacaoSuite) TestRequisicaoAcimaDoLimiteRespondeAMensagemExigida() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP, ""))

	_, corpo := s.chamar("")

	s.Equal(mensagemDoDesafio, corpo, "o corpo do 429 é exigido literalmente pelo desafio")
}

func (s *AceitacaoSuite) TestContagemEhGravadaNoRedisComOTempoDeVidaDaJanela() {
	s.chamar("")

	chaves := s.chaves("ratelimit:count:ip:*")
	s.Require().Len(chaves, 1, "a contagem do IP não apareceu no Redis")

	total, err := s.client.Get(s.ctx, chaves[0]).Int64()
	s.Require().NoError(err)
	s.Equal(int64(1), total)

	ttl, err := s.client.PTTL(s.ctx, chaves[0]).Result()
	s.Require().NoError(err)
	s.Positive(ttl)
	s.LessOrEqual(ttl, janelaDeTeste)
}

func (s *AceitacaoSuite) TestBloqueioEhGravadoNoRedisComOTempoDeVidaDaPunicao() {
	s.chamarVezes(limitePorIP+1, "")

	chaves := s.chaves("ratelimit:block:ip:*")
	s.Require().Len(chaves, 1, "a marca de bloqueio não apareceu no Redis")

	ttl, err := s.client.PTTL(s.ctx, chaves[0]).Result()
	s.Require().NoError(err)
	s.Greater(ttl, janelaDeTeste, "o bloqueio precisa durar mais que a janela para ter efeito")
	s.LessOrEqual(ttl, bloqueioDeTest)
}

func (s *AceitacaoSuite) TestTokenComLimiteMaiorSobrepoeOLimiteDoIP() {
	aceitas := s.chamarVezes(limiteGeneroso, tokenGeneroso)

	s.Equal(limiteGeneroso, aceitas, "o token não conseguiu passar do limite do IP")
	s.Empty(s.chaves("ratelimit:count:ip:*"), "com token cadastrado, o contador do IP não deve nem existir")
}

func (s *AceitacaoSuite) TestTokenComLimiteMenorTambemValeSobreOIP() {
	s.Equal(limiteRestrito, s.chamarVezes(limitePorIP, tokenRestrito))

	status, corpo := s.chamar(tokenRestrito)
	s.Equal(http.StatusTooManyRequests, status)
	s.Equal(mensagemDoDesafio, corpo)
}

// Se a precedência fosse consultada depois do bloqueio, e não antes, o token
// herdaria a punição do IP e o limite maior não valeria de nada.
func (s *AceitacaoSuite) TestTokenContinuaAteOLimiteDeleComOIPJaBloqueado() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP+1, ""), "preparo: o IP precisa estourar")
	s.Require().Len(s.chaves("ratelimit:block:ip:*"), 1, "preparo: o IP precisa estar bloqueado")

	aceitas := s.chamarVezes(limiteGeneroso, tokenGeneroso)

	s.Equal(limiteGeneroso, aceitas, "o token parou antes do limite dele por causa do bloqueio do IP")
}

func (s *AceitacaoSuite) TestIPContinuaAtendendoComUmTokenJaBloqueado() {
	s.Require().Equal(limiteRestrito, s.chamarVezes(limiteRestrito+1, tokenRestrito), "preparo: o token precisa estourar")
	s.Require().Len(s.chaves("ratelimit:block:token:*"), 1, "preparo: o token precisa estar bloqueado")

	// Requisição com token não incrementa o contador do IP, então o IP chega
	// aqui zerado e com o limite dele inteiro disponível.
	s.Equal(limitePorIP, s.chamarVezes(limitePorIP, ""), "o bloqueio do token vazou para o IP")
}

func (s *AceitacaoSuite) TestUmTokenBloqueadoNaoDerrubaOutroToken() {
	s.chamarVezes(limiteRestrito+1, tokenRestrito)
	s.Require().Len(s.chaves("ratelimit:block:token:*"), 1)

	status, _ := s.chamar(tokenGeneroso)

	s.Equal(http.StatusOK, status, "o bloqueio de um token vazou para outro")
}

func (s *AceitacaoSuite) TestRequisicoesConcorrentesNaoPassamDoLimite() {
	const simultaneas = 60

	var (
		grupo   sync.WaitGroup
		mu      sync.Mutex
		aceitas int
	)

	largada := make(chan struct{})
	for i := 0; i < simultaneas; i++ {
		grupo.Add(1)
		go func() {
			defer grupo.Done()

			<-largada
			if status, _ := s.chamar(tokenGeneroso); status == http.StatusOK {
				mu.Lock()
				aceitas++
				mu.Unlock()
			}
		}()
	}

	close(largada)
	grupo.Wait()

	s.Equal(limiteGeneroso, aceitas, "a contagem não segurou o limite sob concorrência")
}
