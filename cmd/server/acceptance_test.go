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

// AceitacaoSuite exercita o sistema inteiro como o avaliador vai exercitar:
// servidor HTTP de verdade, Redis de verdade, e a mesma montagem de middlewares
// que o main usa. As outras suítes provam as peças; esta prova o que o
// docs/desafio.md pede.
type AceitacaoSuite struct {
	suite.Suite
	servidor *httptest.Server
	client   *redis.Client
	ctx      context.Context
}

const (
	// Janela folgada nos testes de contagem: com 1s, uma execução que cruzasse a
	// virada do segundo reiniciaria o contador no meio do teste. O bloqueio é
	// maior que a janela, como em produção — bloqueio mais curto que a janela
	// expiraria antes dela e não teria efeito nenhum.
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

// SetupTest zera o Redis porque todas as requisições saem do mesmo IP local: sem
// isso o contador de um teste condenaria o seguinte.
func (s *AceitacaoSuite) SetupTest() {
	s.Require().NoError(s.client.FlushDB(s.ctx).Err())
}

// chamar faz uma requisição de verdade e devolve status e corpo.
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

// chamarVezes devolve quantas requisições foram aceitas em n tentativas.
func (s *AceitacaoSuite) chamarVezes(n int, token string) (aceitas int) {
	s.T().Helper()

	for i := 0; i < n; i++ {
		if status, _ := s.chamar(token); status == http.StatusOK {
			aceitas++
		}
	}
	return aceitas
}

// chaves lista as chaves que o limiter criou no Redis.
func (s *AceitacaoSuite) chaves(padrao string) []string {
	s.T().Helper()

	encontradas, err := s.client.Keys(s.ctx, padrao).Result()
	s.Require().NoError(err)

	return encontradas
}

// 1. Acesso ao Redis — sem ele o sistema não atende.
func (s *AceitacaoSuite) TestSistemaAtendeComORedisAcessivel() {
	s.Require().NoError(s.client.Ping(s.ctx).Err(), "o Redis do teste não respondeu")

	status, corpo := s.chamar("")

	s.Equal(http.StatusOK, status)
	s.Equal("ok", corpo)
}

// 2. Quantidade de chamados — o limite é exato, não aproximado.
func (s *AceitacaoSuite) TestPassamExatamenteAsRequisicoesDoLimiteDoIP() {
	s.Equal(limitePorIP, s.chamarVezes(limitePorIP+5, ""), "o número de requisições aceitas não bateu com o limite do IP")
}

// 3. Erro pedido no docs — o status.
func (s *AceitacaoSuite) TestRequisicaoAcimaDoLimiteResponde429() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP, ""))

	status, _ := s.chamar("")

	s.Equal(http.StatusTooManyRequests, status)
}

// 4. Erro pedido no docs — o corpo, literal.
func (s *AceitacaoSuite) TestRequisicaoAcimaDoLimiteRespondeAMensagemExigida() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP, ""))

	_, corpo := s.chamar("")

	s.Equal(mensagemDoDesafio, corpo, "o corpo do 429 é exigido literalmente pelo desafio")
}

// 5. Acesso ao Redis — a contagem realmente mora lá, com o tempo de vida da janela.
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

// 6. Acesso ao Redis — o bloqueio é outra chave, com o tempo de vida da punição.
func (s *AceitacaoSuite) TestBloqueioEhGravadoNoRedisComOTempoDeVidaDaPunicao() {
	s.chamarVezes(limitePorIP+1, "")

	chaves := s.chaves("ratelimit:block:ip:*")
	s.Require().Len(chaves, 1, "a marca de bloqueio não apareceu no Redis")

	ttl, err := s.client.PTTL(s.ctx, chaves[0]).Result()
	s.Require().NoError(err)
	s.Greater(ttl, janelaDeTeste, "o bloqueio precisa durar mais que a janela para ter efeito")
	s.LessOrEqual(ttl, bloqueioDeTest)
}

// 7. Quantidade de chamados — a regra de ouro do desafio: o token sobrepõe o IP.
func (s *AceitacaoSuite) TestTokenComLimiteMaiorSobrepoeOLimiteDoIP() {
	aceitas := s.chamarVezes(limiteGeneroso, tokenGeneroso)

	s.Equal(limiteGeneroso, aceitas, "o token não conseguiu passar do limite do IP")
	s.Empty(s.chaves("ratelimit:count:ip:*"), "com token cadastrado, o contador do IP não deve nem existir")
}

// 8. Quantidade de chamados — a precedência aperta tanto quanto afrouxa.
func (s *AceitacaoSuite) TestTokenComLimiteMenorTambemValeSobreOIP() {
	s.Equal(limiteRestrito, s.chamarVezes(limitePorIP, tokenRestrito))

	status, corpo := s.chamar(tokenRestrito)
	s.Equal(http.StatusTooManyRequests, status)
	s.Equal(mensagemDoDesafio, corpo)
}

// 9a. A regra de ouro do desafio no caso que mais dói: o IP já estourou e está
// cumprindo bloqueio, e mesmo assim o token continua até o limite dele. Se a
// precedência fosse consultada depois do bloqueio, e não antes, o token herdaria
// a punição do IP e o limite maior não valeria de nada.
func (s *AceitacaoSuite) TestTokenContinuaAteOLimiteDeleComOIPJaBloqueado() {
	s.Require().Equal(limitePorIP, s.chamarVezes(limitePorIP+1, ""), "preparo: o IP precisa estourar")
	s.Require().Len(s.chaves("ratelimit:block:ip:*"), 1, "preparo: o IP precisa estar bloqueado")

	aceitas := s.chamarVezes(limiteGeneroso, tokenGeneroso)

	s.Equal(limiteGeneroso, aceitas, "o token parou antes do limite dele por causa do bloqueio do IP")
}

// 9b. A independência vale nos dois sentidos: token punido não contamina o IP.
func (s *AceitacaoSuite) TestIPContinuaAtendendoComUmTokenJaBloqueado() {
	s.Require().Equal(limiteRestrito, s.chamarVezes(limiteRestrito+1, tokenRestrito), "preparo: o token precisa estourar")
	s.Require().Len(s.chaves("ratelimit:block:token:*"), 1, "preparo: o token precisa estar bloqueado")

	// Requisição com token não incrementa o contador do IP, então o IP chega
	// aqui zerado e com o limite dele inteiro disponível.
	s.Equal(limitePorIP, s.chamarVezes(limitePorIP, ""), "o bloqueio do token vazou para o IP")
}

// 9c. Identidades distintas não se contaminam.
func (s *AceitacaoSuite) TestUmTokenBloqueadoNaoDerrubaOutroToken() {
	s.chamarVezes(limiteRestrito+1, tokenRestrito)
	s.Require().Len(s.chaves("ratelimit:block:token:*"), 1)

	status, _ := s.chamar(tokenGeneroso)

	s.Equal(http.StatusOK, status, "o bloqueio de um token vazou para outro")
}

// 10. Quantidade de chamados sob concorrência — é o cenário real, e é onde uma
// contagem não atômica deixaria passar mais que o limite.
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
