package tokenmonitor

import (
	"context"
	"sync"
	"time"
)

type HolderSource interface {
	HolderCount(ctx context.Context, mint string) (int, error)
}

// NoopHolderSource é usada enquanto não existe fonte de holders.
// Marca o valor como desconhecido em vez de inventar um número.
type NoopHolderSource struct{}

func (NoopHolderSource) HolderCount(context.Context, string) (int, error) {
	return 0, nil
}

type cachedHolderSource struct {
	inner HolderSource
	ttl   time.Duration
	mu    sync.Mutex
	vals  map[string]cachedHolderVal
}

type cachedHolderVal struct {
	count int
	at    time.Time
}

func NewCachedHolderSource(inner HolderSource, ttl time.Duration) HolderSource {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &cachedHolderSource{
		inner: inner,
		ttl:   ttl,
		vals:  make(map[string]cachedHolderVal),
	}
}

func (c *cachedHolderSource) HolderCount(ctx context.Context, mint string) (int, error) {
	c.mu.Lock()
	if v, ok := c.vals[mint]; ok && time.Since(v.at) < c.ttl {
		c.mu.Unlock()
		return v.count, nil
	}
	c.mu.Unlock()

	count, err := c.inner.HolderCount(ctx, mint)
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	c.vals[mint] = cachedHolderVal{count: count, at: time.Now()}
	c.mu.Unlock()
	return count, nil
}
