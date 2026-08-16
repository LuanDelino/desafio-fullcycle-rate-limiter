package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter"
	"github.com/luanperes/fullcycle-rate-limiter/internal/middleware"
)

// checkerFake registra o que o middleware extraiu da requisição e devolve
// o veredito combinado pelo teste.
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

// checkerEmPanico simula o limiter estourando em runtime.
type checkerEmPanico struct{}

func (checkerEmPanico) Allow(context.Context, string, string) (limiter.Result, error) {
	panic("limiter quebrou")
}

func serve(t *testing.T, checker middleware.Checker, req *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	chegou := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chegou = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	middleware.RateLimit(checker)(next).ServeHTTP(rec, req)
	return rec, chegou
}

func TestRequisicaoPermitidaChegaNoHandler(t *testing.T) {
	checker := &checkerFake{result: limiter.Result{Allowed: true}}

	rec, chegou := serve(t, checker, httptest.NewRequest(http.MethodGet, "/", nil))

	if !chegou {
		t.Fatal("requisição permitida não chegou ao handler seguinte")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, esperado %d", rec.Code, http.StatusOK)
	}
}

func TestRequisicaoBloqueadaResponde429ComAMensagemDoDesafio(t *testing.T) {
	checker := &checkerFake{result: limiter.Result{Allowed: false}}

	rec, chegou := serve(t, checker, httptest.NewRequest(http.MethodGet, "/", nil))

	if chegou {
		t.Fatal("requisição bloqueada chegou ao handler seguinte")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, esperado %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Body.String(); got != middleware.BlockedMessage {
		t.Errorf("corpo = %q, esperado exatamente %q", got, middleware.BlockedMessage)
	}
}

func TestTokenEhLidoDoHeaderAPIKey(t *testing.T) {
	checker := &checkerFake{result: limiter.Result{Allowed: true}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("API_KEY", "abc123")
	serve(t, checker, req)

	if checker.gotTok != "abc123" {
		t.Errorf("token lido = %q, esperado %q", checker.gotTok, "abc123")
	}
}

func TestIPEhExtraidoSemAPortaDeOrigem(t *testing.T) {
	checker := &checkerFake{result: limiter.Result{Allowed: true}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.0.7:54321"
	serve(t, checker, req)

	// A porta muda a cada conexão: mantê-la daria um contador novo por
	// requisição e o limite por IP nunca fecharia.
	if checker.gotIP != "192.168.0.7" {
		t.Errorf("IP extraído = %q, esperado %q", checker.gotIP, "192.168.0.7")
	}
}

func TestFalhaDoLimiterResponde500ENaoAMensagemDeLimite(t *testing.T) {
	checker := &checkerFake{err: errors.New("redis fora do ar")}

	rec, chegou := serve(t, checker, httptest.NewRequest(http.MethodGet, "/", nil))

	if chegou {
		t.Fatal("falha do limiter deixou a requisição passar")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, esperado %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), middleware.BlockedMessage) {
		t.Error("falha de infraestrutura foi reportada como limite excedido")
	}
}
