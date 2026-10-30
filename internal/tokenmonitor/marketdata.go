package tokenmonitor

import (
	"context"
	"sync"
)

type MarketData struct {
	mu     sync.RWMutex
	marks  map[string]float64 // mint -> market cap em SOL
	onMark func(mint string, mcap float64)
}

func NewMarketData() *MarketData {
	return &MarketData{marks: make(map[string]float64)}
}

func (m *MarketData) SetCallback(f func(mint string, mcap float64)) {
	m.mu.Lock()
	m.onMark = f
	m.mu.Unlock()
}

func (m *MarketData) Update(mint string, mcapSOL float64) {
	m.mu.Lock()
	m.marks[mint] = mcapSOL
	cb := m.onMark
	m.mu.Unlock()
	if cb != nil {
		cb(mint, mcapSOL)
	}
}

func (m *MarketData) Get(mint string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.marks[mint]
	return v, ok
}

// StartPoller é um placeholder. O market data vem do WebSocket da PumpPortal;
// não há RPC próprio nem polling externo.
func (m *MarketData) StartPoller(ctx context.Context) {
	<-ctx.Done()
}