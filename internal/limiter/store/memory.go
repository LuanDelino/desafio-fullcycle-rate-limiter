// Package store reúne as implementações de persistência do limiter.
package store

import (
	"context"
	"sync"
	"time"
)

// Memory serve para teste e para rodar sem Redis. Não sobrevive a reinício nem
// vale para mais de uma instância da aplicação.
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

func NewMemory() *Memory {
	return NewMemoryWithClock(time.Now)
}

// NewMemoryWithClock injeta o relógio, o que permite testar janela e bloqueio
// sem esperar tempo real.
func NewMemoryWithClock(now func() time.Time) *Memory {
	return &Memory{
		counters: make(map[string]*counter),
		blocks:   make(map[string]time.Time),
		now:      now,
	}
}

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

func (m *Memory) Block(_ context.Context, key string, duration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blocks[key] = m.now().Add(duration)
	return nil
}

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
