package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/limiter"
	"github.com/LuanDelino/desafio-fullcycle-rate-limiter/internal/middleware"
)

type checkerFake struct {
	result limiter.Result
	err    error
	gotIP  string
	gotTok string
}

func (c *checkerFake) Allow(_ context.Context, ip, token string) (limiter.Result, error) {
	c.gotIP, c.gotTok = ip, token
	return c.result, c.err
}

type checkerEmPanico struct{}

func (checkerEmPanico) Allow(context.Context, string, string) (limiter.Result, error) {
	panic("limiter quebrou")
}

type RateLimitSuite struct {
	suite.Suite
	checker  *checkerFake
	chegou   bool
	recorder *httptest.ResponseRecorder
}

func TestRateLimitSuite(t *testing.T) {
	suite.Run(t, new(RateLimitSuite))
}

func (s *RateLimitSuite) SetupTest() {
	s.checker = &checkerFake{}
	s.chegou = false
	s.recorder = httptest.NewRecorder()
}

func (s *RateLimitSuite) serve(req *http.Request) {
	s.T().Helper()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.chegou = true
		w.WriteHeader(http.StatusOK)
	})

	middleware.RateLimit(s.checker)(next).ServeHTTP(s.recorder, req)
}

func (s *RateLimitSuite) TestRequisicaoPermitidaChegaNoHandler() {
	s.checker.result = limiter.Result{Allowed: true}

	s.serve(httptest.NewRequest(http.MethodGet, "/", nil))

	s.True(s.chegou, "requisição permitida não chegou ao handler seguinte")
	s.Equal(http.StatusOK, s.recorder.Code)
}

func (s *RateLimitSuite) TestRequisicaoBloqueadaResponde429ComAMensagemDoDesafio() {
	s.checker.result = limiter.Result{Allowed: false}

	s.serve(httptest.NewRequest(http.MethodGet, "/", nil))

	s.False(s.chegou, "requisição bloqueada chegou ao handler seguinte")
	s.Equal(http.StatusTooManyRequests, s.recorder.Code)
	s.Equal(middleware.BlockedMessage, s.recorder.Body.String(), "o corpo do 429 é exigido literalmente pelo desafio")
}

func (s *RateLimitSuite) TestTokenEhLidoDoHeaderAPIKey() {
	s.checker.result = limiter.Result{Allowed: true}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("API_KEY", "abc123")
	s.serve(req)

	s.Equal("abc123", s.checker.gotTok)
}

func (s *RateLimitSuite) TestIPEhExtraidoSemAPortaDeOrigem() {
	s.checker.result = limiter.Result{Allowed: true}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.0.7:54321"
	s.serve(req)

	// A porta muda a cada conexão: mantê-la daria um contador novo por
	// requisição e o limite por IP nunca fecharia.
	s.Equal("192.168.0.7", s.checker.gotIP)
}

func (s *RateLimitSuite) TestFalhaDoLimiterResponde500ENaoAMensagemDeLimite() {
	s.checker.err = errors.New("redis fora do ar")

	s.serve(httptest.NewRequest(http.MethodGet, "/", nil))

	s.False(s.chegou, "falha do limiter deixou a requisição passar")
	s.Equal(http.StatusInternalServerError, s.recorder.Code)
	s.NotContains(s.recorder.Body.String(), middleware.BlockedMessage, "falha de infraestrutura foi reportada como limite excedido")
}
