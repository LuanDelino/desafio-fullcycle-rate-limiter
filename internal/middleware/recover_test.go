package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/luanperes/fullcycle-rate-limiter/internal/middleware"
)

type RecoverSuite struct {
	suite.Suite
	recorder *httptest.ResponseRecorder
}

func TestRecoverSuite(t *testing.T) {
	suite.Run(t, new(RecoverSuite))
}

func (s *RecoverSuite) SetupTest() {
	s.recorder = httptest.NewRecorder()
}

// serve roda a requisição pelo Recover envolvendo o handler informado.
func (s *RecoverSuite) serve(handler http.Handler) {
	s.T().Helper()

	middleware.Recover(handler).ServeHTTP(s.recorder, httptest.NewRequest(http.MethodGet, "/", nil))
}

func (s *RecoverSuite) TestPanicViraRespostaDeErroENaoDerrubaOServidor() {
	emPanico := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler quebrou")
	})

	s.Require().NotPanics(func() { s.serve(emPanico) }, "o panic escapou do middleware")

	// Sem o middleware o net/http encerraria a conexão sem status: o cliente
	// veria a resposta cortada em vez de um erro.
	s.Equal(http.StatusInternalServerError, s.recorder.Code)
}

func (s *RecoverSuite) TestRequisicaoNormalAtravessaORecoverIntacta() {
	normal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("corpo original"))
	})

	s.serve(normal)

	s.Equal(http.StatusTeapot, s.recorder.Code)
	s.Equal("corpo original", s.recorder.Body.String())
}

func (s *RecoverSuite) TestPanicDoLimiterTambemEhRecuperado() {
	limiterEmPanico := middleware.RateLimit(checkerEmPanico{})
	handler := limiterEmPanico(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	s.Require().NotPanics(func() { s.serve(handler) })

	// É por isso que o Recover é o middleware mais externo.
	s.Equal(http.StatusInternalServerError, s.recorder.Code)
}

func (s *RecoverSuite) TestErrAbortHandlerContinuaAbortandoEmSilencio() {
	abortando := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})

	// O net/http trata este panic como pedido explícito de abandonar a resposta;
	// engoli-lo aqui viraria um 500 e um log de erro que ninguém pediu.
	s.PanicsWithValue(http.ErrAbortHandler, func() { s.serve(abortando) })
}
