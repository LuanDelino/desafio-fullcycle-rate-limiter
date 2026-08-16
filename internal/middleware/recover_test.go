package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luanperes/fullcycle-rate-limiter/internal/middleware"
)

func TestPanicViraRespostaDeErroENaoDerrubaOServidor(t *testing.T) {
	handler := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler quebrou")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// Sem o middleware o net/http encerraria a conexão sem status: o cliente
	// veria a resposta cortada em vez de um erro.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, esperado %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestRequisicaoNormalAtravessaORecoverIntacta(t *testing.T) {
	handler := middleware.Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("corpo original"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, esperado %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Body.String(); got != "corpo original" {
		t.Errorf("corpo = %q, esperado %q", got, "corpo original")
	}
}

func TestPanicDoLimiterTambemEhRecuperado(t *testing.T) {
	limiterEmPanico := middleware.RateLimit(checkerEmPanico{})
	handler := middleware.Recover(limiterEmPanico(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {},
	)))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// É por isso que o Recover é o middleware mais externo.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, esperado %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestErrAbortHandlerContinuaAbortandoEmSilencio(t *testing.T) {
	handler := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("panic propagado = %v, esperado http.ErrAbortHandler", recovered)
		}
	}()

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("ErrAbortHandler foi engolido pelo Recover")
}
