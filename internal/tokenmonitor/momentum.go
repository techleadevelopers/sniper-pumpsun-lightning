package tokenmonitor

import (
	"sync"
	"time"
)

type TradeEvent struct {
	Mint      string
	TxType    string // "buy" | "sell"
	SOLAmount float64
	Trader    string
	At        time.Time
}

type MomentumConfig struct {
	ShortWindow     time.Duration
	MediumWindow    time.Duration
	LongWindow      time.Duration
	MinVolumeSOL    float64
	MinBuyRatio     float64
	MinUniqueBuyers int
	MinAcceleration float64
	MaxTradeAge     time.Duration
}

type MomentumSnapshot struct {
	VolumeShort  float64
	VolumeMedium float64
	VolumeLong   float64
	BuyRatio     float64
	UniqueBuyers int
	Acceleration float64
	TradeCount   int
	Evaluated    bool
}

type MomentumTracker struct {
	cfg  MomentumConfig
	mu   sync.Mutex
	data map[string][]TradeEvent
}

func NewMomentumTracker(cfg MomentumConfig) *MomentumTracker {
	return &MomentumTracker{cfg: cfg, data: make(map[string][]TradeEvent)}
}

func (m *MomentumTracker) Record(ev TradeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[ev.Mint] = append(m.data[ev.Mint], ev)

	// Limpeza simples para não crescer indefinidamente.
	if len(m.data[ev.Mint]) > 5000 {
		m.data[ev.Mint] = m.data[ev.Mint][len(m.data[ev.Mint])-2500:]
	}
}

func (m *MomentumTracker) Evaluate(mint string, now time.Time) MomentumSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	events := m.data[mint]
	var s MomentumSnapshot
	if len(events) == 0 {
		return s
	}

	cutoffLong := now.Add(-m.cfg.LongWindow)
	cutoffMedium := now.Add(-m.cfg.MediumWindow)
	cutoffShort := now.Add(-m.cfg.ShortWindow)

	buyers := make(map[string]struct{})
	var buys, sells float64

	for _, ev := range events {
		if ev.At.Before(cutoffLong) {
			continue
		}
		s.TradeCount++
		if ev.TxType == "buy" {
			buys += ev.SOLAmount
			buyers[ev.Trader] = struct{}{}
		} else {
			sells += ev.SOLAmount
		}
		s.VolumeLong += ev.SOLAmount
		if ev.At.After(cutoffMedium) {
			s.VolumeMedium += ev.SOLAmount
		}
		if ev.At.After(cutoffShort) {
			s.VolumeShort += ev.SOLAmount
		}
	}

	total := buys + sells
	if total > 0 {
		s.BuyRatio = buys / total
	}
	s.UniqueBuyers = len(buyers)

	if m.cfg.ShortWindow > 0 && m.cfg.MediumWindow > m.cfg.ShortWindow {
		rateShort := s.VolumeShort / m.cfg.ShortWindow.Seconds()
		rateMedium := s.VolumeMedium / m.cfg.MediumWindow.Seconds()
		if rateMedium > 0 {
			s.Acceleration = (rateShort - rateMedium) / rateMedium
		}
	}

	s.Evaluated = true
	return s
}

func (m *MomentumTracker) Forget(mint string) {
	m.mu.Lock()
	delete(m.data, mint)
	m.mu.Unlock()
}
