// Package config lê todas as definições do ambiente. Nenhum outro pacote
// consulta variável de ambiente: o que o sistema lê está registrado aqui.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config é o conjunto completo de definições da aplicação.
type Config struct {
	// ServerPort é a porta HTTP do servidor.
	ServerPort string
	// Store escolhe a estratégia de persistência: "redis" ou "memory".
	Store string
	// RedisAddr, RedisPassword e RedisDB configuram a conexão com o Redis.
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	// IPLimit é o máximo de requisições por janela para um mesmo IP.
	IPLimit int64
	// TokenLimits mapeia token -> máximo de requisições por janela.
	TokenLimits map[string]int64
	// Window é o tamanho da janela de contagem.
	Window time.Duration
	// BlockDuration é o tempo de bloqueio de quem estourou o limite.
	BlockDuration time.Duration
}

// Load lê o .env da raiz, se existir, e monta a configuração a partir do
// ambiente. Ausência do arquivo não é erro: em container as variáveis vêm
// do próprio ambiente.
func Load() (Config, error) {
	_ = godotenv.Load()

	ipLimit, err := envInt64("RATE_LIMIT_IP", 10)
	if err != nil {
		return Config{}, err
	}
	redisDB, err := envInt("REDIS_DB", 0)
	if err != nil {
		return Config{}, err
	}
	window, err := envDuration("RATE_LIMIT_WINDOW", time.Second)
	if err != nil {
		return Config{}, err
	}
	blockDuration, err := envDuration("RATE_LIMIT_BLOCK_DURATION", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	tokenLimits, err := parseTokenLimits(os.Getenv("RATE_LIMIT_TOKENS"))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ServerPort:    envString("WEB_SERVER_PORT", "8080"),
		Store:         envString("RATE_LIMIT_STORE", "redis"),
		RedisAddr:     envString("REDIS_ADDR", "localhost:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisDB:       redisDB,
		IPLimit:       ipLimit,
		TokenLimits:   tokenLimits,
		Window:        window,
		BlockDuration: blockDuration,
	}

	if cfg.Store != "redis" && cfg.Store != "memory" {
		return Config{}, fmt.Errorf("RATE_LIMIT_STORE inválido: %q (use redis ou memory)", cfg.Store)
	}
	if cfg.IPLimit <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_IP deve ser maior que zero, veio %d", cfg.IPLimit)
	}

	return cfg, nil
}

// parseTokenLimits lê o formato "token:limite,outro:limite".
func parseTokenLimits(raw string) (map[string]int64, error) {
	limits := make(map[string]int64)

	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		token, value, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("RATE_LIMIT_TOKENS: par %q não está no formato token:limite", pair)
		}

		token = strings.TrimSpace(token)
		limit, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("RATE_LIMIT_TOKENS: limite do token %q inválido: %w", token, err)
		}
		if token == "" || limit <= 0 {
			return nil, fmt.Errorf("RATE_LIMIT_TOKENS: par %q precisa de token e limite positivo", pair)
		}

		limits[token] = limit
	}

	return limits, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v, err := envInt64(key, int64(fallback))
	return int(v), err
}

func envInt64(key string, fallback int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s inválido: %w", key, err)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s inválido: %w", key, err)
	}
	return v, nil
}
