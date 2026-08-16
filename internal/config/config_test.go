package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/luanperes/fullcycle-rate-limiter/internal/config"
)

type ConfigSuite struct {
	suite.Suite
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

// SetupTest zera as variáveis que a suíte manipula. t.Setenv restaura o valor
// anterior ao fim de cada teste, então um teste não vaza ambiente no seguinte.
func (s *ConfigSuite) SetupTest() {
	for _, nome := range []string{
		"WEB_SERVER_PORT", "RATE_LIMIT_STORE", "REDIS_ADDR", "REDIS_PASSWORD",
		"REDIS_DB", "RATE_LIMIT_IP", "RATE_LIMIT_TOKENS", "RATE_LIMIT_WINDOW",
		"RATE_LIMIT_BLOCK_DURATION",
	} {
		s.T().Setenv(nome, "")
	}
}

func (s *ConfigSuite) TestAmbienteVazioCaiNosPadroes() {
	cfg, err := config.Load()

	s.Require().NoError(err)
	s.Equal("8080", cfg.ServerPort)
	s.Equal("redis", cfg.Store)
	s.Equal("localhost:6379", cfg.RedisAddr)
	s.Equal(int64(10), cfg.IPLimit)
	s.Equal(time.Second, cfg.Window)
	s.Equal(5*time.Minute, cfg.BlockDuration)
	s.Empty(cfg.TokenLimits)
}

func (s *ConfigSuite) TestLeTodosOsCamposDoAmbiente() {
	s.T().Setenv("WEB_SERVER_PORT", "9090")
	s.T().Setenv("RATE_LIMIT_STORE", "memory")
	s.T().Setenv("REDIS_ADDR", "redis:6379")
	s.T().Setenv("REDIS_PASSWORD", "segredo")
	s.T().Setenv("REDIS_DB", "3")
	s.T().Setenv("RATE_LIMIT_IP", "42")
	s.T().Setenv("RATE_LIMIT_WINDOW", "500ms")
	s.T().Setenv("RATE_LIMIT_BLOCK_DURATION", "1h")

	cfg, err := config.Load()

	s.Require().NoError(err)
	s.Equal("9090", cfg.ServerPort)
	s.Equal("memory", cfg.Store)
	s.Equal("redis:6379", cfg.RedisAddr)
	s.Equal("segredo", cfg.RedisPassword)
	s.Equal(3, cfg.RedisDB)
	s.Equal(int64(42), cfg.IPLimit)
	s.Equal(500*time.Millisecond, cfg.Window)
	s.Equal(time.Hour, cfg.BlockDuration)
}

func (s *ConfigSuite) TestParseDosLimitesPorToken() {
	casos := []struct {
		nome     string
		valor    string
		esperado map[string]int64
	}{
		{"vazio não cadastra token", "", map[string]int64{}},
		{"um par", "abc123:100", map[string]int64{"abc123": 100}},
		{"vários pares", "abc123:100,def456:5", map[string]int64{"abc123": 100, "def456": 5}},
		{"espaços em volta são ignorados", " abc123 : 100 , def456:5 ", map[string]int64{"abc123": 100, "def456": 5}},
		{"vírgula sobrando é ignorada", "abc123:100,", map[string]int64{"abc123": 100}},
	}

	for _, caso := range casos {
		s.Run(caso.nome, func() {
			s.T().Setenv("RATE_LIMIT_TOKENS", caso.valor)

			cfg, err := config.Load()

			s.Require().NoError(err)
			s.Equal(caso.esperado, cfg.TokenLimits)
		})
	}
}

func (s *ConfigSuite) TestValorInvalidoDerrubaASubidaEmVezDeCairNoPadrao() {
	casos := []struct {
		nome     string
		variavel string
		valor    string
		noErro   string
	}{
		{"store desconhecido", "RATE_LIMIT_STORE", "postgres", "RATE_LIMIT_STORE"},
		{"limite de IP não numérico", "RATE_LIMIT_IP", "dez", "RATE_LIMIT_IP"},
		{"limite de IP zerado", "RATE_LIMIT_IP", "0", "RATE_LIMIT_IP"},
		{"limite de IP negativo", "RATE_LIMIT_IP", "-1", "RATE_LIMIT_IP"},
		{"janela sem unidade", "RATE_LIMIT_WINDOW", "1000", "RATE_LIMIT_WINDOW"},
		{"bloqueio sem unidade", "RATE_LIMIT_BLOCK_DURATION", "5", "RATE_LIMIT_BLOCK_DURATION"},
		{"banco do redis não numérico", "REDIS_DB", "primeiro", "REDIS_DB"},
		{"token sem limite", "RATE_LIMIT_TOKENS", "abc123", "RATE_LIMIT_TOKENS"},
		{"limite do token não numérico", "RATE_LIMIT_TOKENS", "abc123:muitas", "RATE_LIMIT_TOKENS"},
		{"limite do token zerado", "RATE_LIMIT_TOKENS", "abc123:0", "RATE_LIMIT_TOKENS"},
		{"token sem nome", "RATE_LIMIT_TOKENS", ":100", "RATE_LIMIT_TOKENS"},
	}

	for _, caso := range casos {
		s.Run(caso.nome, func() {
			s.T().Setenv(caso.variavel, caso.valor)

			_, err := config.Load()

			// Cair no padrão em silêncio seria pior que não subir: o operador
			// acharia que configurou e o limite valendo seria outro.
			s.Require().Error(err)
			s.ErrorContains(err, caso.noErro, "o erro precisa dizer qual variável está errada")
		})
	}
}
