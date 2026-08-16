// Package middleware liga o limiter ao servidor HTTP. Aqui só mora tradução de
// requisição para chamada de negócio e de veredito para resposta.
package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"

	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter"
)

// TokenHeader é o header lido para identificar o token de acesso.
const TokenHeader = "API_KEY"

// BlockedMessage é o corpo exigido pelo desafio na resposta 429.
const BlockedMessage = "you have reached the maximum number of requests or actions allowed within a certain time frame"

// Checker é o que o middleware precisa do limiter, declarado aqui no consumidor
// para o middleware poder ser testado com um duplo.
type Checker interface {
	Allow(ctx context.Context, ip, token string) (limiter.Result, error)
}

// RateLimit devolve o middleware que aplica o limiter em cada requisição.
func RateLimit(checker Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			result, err := checker.Allow(r.Context(), ip, r.Header.Get(TokenHeader))
			if err != nil {
				// Falha do store não pode virar liberação silenciosa nem
				// mensagem de limite: é erro do servidor e é registrado.
				slog.Error("rate limiter indisponível", "erro", err, "ip", ip)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			if !result.Allowed {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(BlockedMessage))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extrai o IP de quem chamou, descartando a porta de origem: ela muda
// a cada conexão e daria um contador novo por requisição.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
