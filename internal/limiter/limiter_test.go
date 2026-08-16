package limiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter"
	"github.com/luanperes/fullcycle-rate-limiter/internal/limiter/store"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newFixture(t *testing.T, cfg limiter.Config) (*limiter.Limiter, *clock) {
	t.Helper()

	c := &clock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}
	return limiter.New(store.NewMemoryWithClock(c.Now), cfg), c
}

func allow(t *testing.T, l *limiter.Limiter, ip, token string) limiter.Result {
	t.Helper()

	result, err := l.Allow(context.Background(), ip, token)
	if err != nil {
		t.Fatalf("Allow(%q, %q) devolveu erro: %v", ip, token, err)
	}
	return result
}

func TestPermiteAteOLimiteDoIPEBloqueiaODepois(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       3,
		Window:        time.Second,
		BlockDuration: 5 * time.Minute,
	})

	for i := 1; i <= 3; i++ {
		if result := allow(t, l, "10.0.0.1", ""); !result.Allowed {
			t.Fatalf("requisição %d dentro do limite foi rejeitada", i)
		}
	}

	if result := allow(t, l, "10.0.0.1", ""); result.Allowed {
		t.Fatal("requisição acima do limite do IP foi permitida")
	}
}

func TestIPsDiferentesTemContadoresIndependentes(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       1,
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	allow(t, l, "10.0.0.1", "")

	if result := allow(t, l, "10.0.0.2", ""); !result.Allowed {
		t.Fatal("o estouro de um IP afetou o contador de outro IP")
	}
}

func TestLimiteDoTokenSeSobrepoeAoDoIP(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       2,
		TokenLimits:   map[string]int64{"abc123": 5},
		Window:        time.Second,
		BlockDuration: 5 * time.Minute,
	})

	// O mesmo IP passa das 2 do limite de IP porque o token cadastrado vale 5.
	for i := 1; i <= 5; i++ {
		if result := allow(t, l, "10.0.0.1", "abc123"); !result.Allowed {
			t.Fatalf("requisição %d do token foi rejeitada antes do limite dele", i)
		}
	}

	if result := allow(t, l, "10.0.0.1", "abc123"); result.Allowed {
		t.Fatal("requisição acima do limite do token foi permitida")
	}
}

func TestTokenMaisRestritivoQueOIPValeMesmoAssim(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       100,
		TokenLimits:   map[string]int64{"restrito": 1},
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	allow(t, l, "10.0.0.1", "restrito")

	if result := allow(t, l, "10.0.0.1", "restrito"); result.Allowed {
		t.Fatal("token mais restritivo que o IP não foi respeitado")
	}
}

func TestTokenNaoCadastradoCaiNoLimiteDoIP(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       1,
		TokenLimits:   map[string]int64{"abc123": 100},
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	allow(t, l, "10.0.0.1", "token-inventado")

	if result := allow(t, l, "10.0.0.1", "token-inventado"); result.Allowed {
		t.Fatal("token não cadastrado escapou do limite do IP")
	}
}

func TestContadorReiniciaNaJanelaSeguinte(t *testing.T) {
	l, c := newFixture(t, limiter.Config{
		IPLimit:       2,
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	allow(t, l, "10.0.0.1", "")
	allow(t, l, "10.0.0.1", "")
	c.Advance(time.Second)

	if result := allow(t, l, "10.0.0.1", ""); !result.Allowed {
		t.Fatal("contador não reiniciou na janela seguinte")
	}
}

func TestBloqueioSobreviveAoFimDaJanela(t *testing.T) {
	l, c := newFixture(t, limiter.Config{
		IPLimit:       1,
		Window:        time.Second,
		BlockDuration: 5 * time.Minute,
	})

	allow(t, l, "10.0.0.1", "")
	allow(t, l, "10.0.0.1", "") // estoura e bloqueia

	// A janela virou, mas a punição é maior que ela: continua bloqueado.
	c.Advance(2 * time.Second)
	if result := allow(t, l, "10.0.0.1", ""); result.Allowed {
		t.Fatal("nova janela liberou um IP ainda dentro do tempo de bloqueio")
	}

	c.Advance(5 * time.Minute)
	if result := allow(t, l, "10.0.0.1", ""); !result.Allowed {
		t.Fatal("IP continuou bloqueado depois do fim do tempo de bloqueio")
	}
}

func TestRemainingDecrescePorRequisicao(t *testing.T) {
	l, _ := newFixture(t, limiter.Config{
		IPLimit:       3,
		Window:        time.Second,
		BlockDuration: time.Minute,
	})

	for i, want := range []int64{2, 1, 0} {
		if got := allow(t, l, "10.0.0.1", "").Remaining; got != want {
			t.Errorf("requisição %d: Remaining = %d, esperado %d", i+1, got, want)
		}
	}
}

// storeQuebrado devolve erro em toda consulta, para provar que a falha de
// persistência sobe como erro em vez de virar liberação silenciosa.
type storeQuebrado struct{}

func (storeQuebrado) Increment(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("store fora do ar")
}
func (storeQuebrado) Block(context.Context, string, time.Duration) error {
	return errors.New("store fora do ar")
}
func (storeQuebrado) Blocked(context.Context, string) (bool, error) {
	return false, errors.New("store fora do ar")
}

func TestFalhaDoStoreViraErroENaoLiberacao(t *testing.T) {
	l := limiter.New(storeQuebrado{}, limiter.Config{IPLimit: 1, Window: time.Second})

	result, err := l.Allow(context.Background(), "10.0.0.1", "")
	if err == nil {
		t.Fatal("falha do store não devolveu erro")
	}
	if result.Allowed {
		t.Fatal("falha do store liberou a requisição")
	}
}
