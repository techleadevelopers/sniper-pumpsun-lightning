package positions

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"sniper-lightning/internal/database"
)

// Seller é o que o Manager precisa do execution.Engine.
// Definir aqui (em vez de importar execution) evita o import cycle:
//   execution -> positions (para PnL/IsStopLoss)
//   positions -> execution (seria ciclo)
// O engine.Sell satisfaz esta interface sem saber dela.
type Seller interface {
	Sell(ctx context.Context, mint string, slippagePct float64, priorityFeeSOL float64, reason string) (string, error)
}

// PriceTracker é o que o Manager precisa do tokenmonitor.
// O monitor.go implementa esta interface (TrackMint/UntrackMint).
type PriceTracker interface {
	TrackMint(mint string)
	UntrackMint(mint string)
}

// Manager corre o ciclo de avaliação de saídas em paper e em live.
type Manager struct {
	db         *database.DB
	seller     Seller
	tracker    PriceTracker
	log        *zap.Logger
	simulation bool
	limits     Limits

	pollInterval   time.Duration
	slippagePct    float64
	priorityFeeSOL float64

	mu       sync.Mutex
	lastSeen map[string]bool
}

type ManagerConfig struct {
	Simulation     bool
	PollInterval   time.Duration
	SlippagePct    float64
	PriorityFeeSOL float64
	Limits         Limits
}

func NewManager(
	db *database.DB,
	seller Seller,
	tracker PriceTracker,
	log *zap.Logger,
	cfg ManagerConfig,
) *Manager {
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Manager{
		db:             db,
		seller:         seller,
		tracker:        tracker,
		log:            log,
		simulation:     cfg.Simulation,
		limits:         cfg.Limits,
		pollInterval:   interval,
		slippagePct:    cfg.SlippagePct,
		priorityFeeSOL: cfg.PriorityFeeSOL,
		lastSeen:       make(map[string]bool),
	}
}

func (m *Manager) Run(ctx context.Context) {
	m.log.Info("position manager iniciado",
		zap.Bool("simulation", m.simulation),
		zap.Duration("poll_interval", m.pollInterval),
	)

	m.syncSubscriptions()

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.log.Info("position manager a desligar")
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) tick(ctx context.Context) {
	m.syncSubscriptions()

	mints, err := m.db.OpenPositionMints()
	if err != nil {
		m.log.Warn("manager: listar posições falhou", zap.Error(err))
		return
	}

	now := time.Now().UTC()

	for _, mint := range mints {
		entry, current, peak, ok, err := m.db.Mark(m.simulation, mint)
		if err != nil {
			m.log.Warn("manager: ler mark falhou",
				zap.String("mint", mint), zap.Error(err))
			continue
		}
		if !ok || entry <= 0 || current <= 0 {
			continue
		}

		cost, openedAt, ok, err := m.db.OpenPositionDetails(mint)
		if err != nil || !ok || cost <= 0 {
			continue
		}

		ratioNow := current / entry
		ratioPeak := peak / entry
		currentValueSOL := cost * ratioNow
		peakValueSOL := cost * ratioPeak

		pos := Position{
			Mint:     mint,
			CostSOL:  cost,
			PeakSOL:  peakValueSOL,
			OpenedAt: openedAt,
		}

		reason := ExitReason(pos, currentValueSOL, true, m.limits, now)
		if reason == "" {
			continue
		}

		m.log.Info("exit trigger",
			zap.String("mint", mint),
			zap.String("reason", reason),
			zap.Float64("cost_sol", cost),
			zap.Float64("current_value_sol", currentValueSOL),
			zap.Float64("peak_value_sol", peakValueSOL),
			zap.Bool("simulation", m.simulation),
		)

		if _, err := m.seller.Sell(ctx, mint, m.slippagePct, m.priorityFeeSOL, reason); err != nil {
			m.log.Error("Sell falhou",
				zap.String("mint", mint),
				zap.String("reason", reason),
				zap.Error(err),
			)
			continue
		}

		if m.tracker != nil {
			m.tracker.UntrackMint(mint)
		}
		m.mu.Lock()
		delete(m.lastSeen, mint)
		m.mu.Unlock()
	}
}

func (m *Manager) syncSubscriptions() {
	if m.tracker == nil {
		return
	}
	open, err := m.db.OpenPositionMints()
	if err != nil {
		return
	}

	openSet := make(map[string]bool, len(open))
	for _, mint := range open {
		openSet[mint] = true
	}

	m.mu.Lock()
	for mint := range openSet {
		if !m.lastSeen[mint] {
			m.tracker.TrackMint(mint)
			m.lastSeen[mint] = true
		}
	}
	for mint := range m.lastSeen {
		if !openSet[mint] {
			m.tracker.UntrackMint(mint)
			delete(m.lastSeen, mint)
		}
	}
	m.mu.Unlock()
}

func (m *Manager) FormatState() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("tracked=%d simulation=%t", len(m.lastSeen), m.simulation)
}
