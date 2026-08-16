package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover transforma panic de handler em 500 registrado.
//
// O net/http já recupera o panic e mantém o servidor de pé, mas encerra a
// conexão sem escrever status: o cliente vê a resposta cortada, não um erro.
// Deve ser o middleware mais externo, para cobrir também o que roda antes do
// handler — o rate limiter inclusive.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}

			// ErrAbortHandler é o pedido explícito do net/http para abandonar a
			// resposta em silêncio.
			if panicValue == http.ErrAbortHandler {
				panic(panicValue)
			}

			slog.Error("panic durante a requisição",
				"panic", panicValue,
				"metodo", r.Method,
				"caminho", r.URL.Path,
				"stack", string(debug.Stack()),
			)

			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()

		next.ServeHTTP(w, r)
	})
}
