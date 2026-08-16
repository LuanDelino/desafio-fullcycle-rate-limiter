package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Olha o mapa por dentro porque o vazamento não aparece em asserção de
// comportamento: as respostas continuam certas enquanto a memória cresce.
// A promessa é teto, não limpeza imediata — a varredura é amortizada.
func TestMemoryNaoCresceSemTetoComIdentidadesQueNaoVoltam(t *testing.T) {
	agora := time.Now()
	m := NewMemoryWithClock(func() time.Time { return agora })
	ctx := context.Background()

	gerarTrafego := func(prefixo string) {
		t.Helper()
		for i := 0; i < limpezaACada; i++ {
			_, err := m.Increment(ctx, prefixo+strconv.Itoa(i), time.Second)
			require.NoError(t, err)
		}
	}

	// Uma leva de identidades que aparecem uma vez e nunca mais — o IP que fez
	// uma requisição e foi embora.
	gerarTrafego("ip:primeira-leva-")
	require.Len(t, m.counters, limpezaACada)

	// Muito depois de a primeira leva vencer, chega uma leva inteiramente nova.
	agora = agora.Add(time.Hour)
	gerarTrafego("ip:segunda-leva-")

	// Sem a varredura o mapa teria as duas levas. O teto é o tráfego vivo.
	m.mu.Lock()
	defer m.mu.Unlock()
	require.LessOrEqualf(t, len(m.counters), limpezaACada,
		"contadores vencidos continuam ocupando memória: %d entradas para %d identidades vivas",
		len(m.counters), limpezaACada)
}

func TestMemoryVarreBloqueiosVencidos(t *testing.T) {
	agora := time.Now()
	m := NewMemoryWithClock(func() time.Time { return agora })
	ctx := context.Background()

	for i := 0; i < limpezaACada; i++ {
		require.NoError(t, m.Block(ctx, "ip:antiga-"+strconv.Itoa(i), time.Second))
	}
	require.Len(t, m.blocks, limpezaACada)

	agora = agora.Add(time.Hour)
	for i := 0; i < limpezaACada; i++ {
		require.NoError(t, m.Block(ctx, "ip:nova-"+strconv.Itoa(i), time.Second))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	require.LessOrEqual(t, len(m.blocks), limpezaACada, "bloqueios vencidos continuam ocupando memória")
}
