// Package store reúne as implementações de persistência do limiter.
package store

import (
	"context"
	"sync"
	"time"
)

// Memory guarda contadores e bloqueios em memória do processo.
// Serve para testes e para rodar o servidor sem Redis; não sobrevive a
// reinício nem funciona com mais de uma instância da aplicação.
type Memory struct {
	mu       sync.Mutex
	counters map[string]*counter
	blocks   map[string]time.Time
	now      func() time.Time
}

type counter struct {
	value     int64
	expiresAt time.Time
}

// NewMemory devolve um store em memória usando o relógio do sistema.
func NewMemory() *Memory {
	return NewMemoryWithClock(time.Now)
}

// NewMemoryWithClock devolve um store em memória com relógio injetável,
// o que permite testar janela e bloqueio sem esperar tempo real.
func NewMemoryWithClock(now func() time.Time) *Memory {
	return &Memory{
		counters: make(map[string]*counter),
		blocks:   make(map[string]time.Time),
		now:      now,
	}
}

// Increment soma 1 no contador da chave, reiniciando quando a janela venceu.
func (m *Memory) Increment(_ context.Context, key string, window time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	c, ok := m.counters[key]
	if !ok || !now.Before(c.expiresAt) {
		m.counters[key] = &counter{value: 1, expiresAt: now.Add(window)}
		return 1, nil
	}

	c.value++
	return c.value, nil
}

// Block marca a chave como bloqueada pelo período informado.
func (m *Memory) Block(_ context.Context, key string, duration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blocks[key] = m.now().Add(duration)
	return nil
}

// Blocked informa se a chave ainda está dentro do período de bloqueio.
func (m *Memory) Blocked(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	until, ok := m.blocks[key]
	if !ok {
		return false, nil
	}
	if !m.now().Before(until) {
		delete(m.blocks, key)
		return false, nil
	}
	return true, nil
}
