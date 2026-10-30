package tokenmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"sniper-lightning/internal/database"
	"sniper-lightning/internal/events"
)

type MonitorConfig struct {
	EntryDelay      time.Duration
	TokenMaxAge     time.Duration
	MinMarketCapSOL float64
	MaxMarketCapSOL float64
	DedupeTTL       time.Duration
	Momentum        MomentumConfig
}

type BuyFn func(
	ctx context.Context,
	mint string,
	creator string,
	mcapSOL float64,
	profile string,
	score float64,
	reason string,
) error

type Monitor struct {
	wsURL  string
	apiKey string
	db     *database.DB
	bus    *events.Bus
	log    *zap.Logger
	cfg    MonitorConfig
	profiles Profiles
	buyFn  BuyFn
	simulation bool

	mu       sync.Mutex
	seen     map[string]time.Time

	tracked map[string]bool

	conn   *websocket.Conn
	connMu sync.Mutex

	mom *MomentumTracker
}

func New(
	wsURL, apiKey string,
	db *database.DB,
	bus *events.Bus,
	log *zap.Logger,
	cfg MonitorConfig,
	profiles Profiles,
	simulation bool,
	buyFn BuyFn,
) *Monitor {
	return &Monitor{
		wsURL:      wsURL,
		apiKey:     apiKey,
		db:         db,
		bus:        bus,
		log:        log,
		cfg:        cfg,
		profiles:   profiles,
		buyFn:      buyFn,
		simulation: simulation,
		seen:       make(map[string]time.Time),
		tracked:    make(map[string]bool),
		mom:        NewMomentumTracker(cfg.Momentum),
	}
}

func (m *Monitor) TrackMint(mint string) {
	m.mu.Lock()
	already := m.tracked[mint]
	m.tracked[mint] = true
	m.mu.Unlock()
	if already {
		return
	}
	m.writeJSON(map[string]any{
		"method": "subscribeTokenTrade",
		"keys":   []string{mint},
	})
	m.log.Info("WS: subscrito a trades", zap.String("mint", mint))
}

func (m *Monitor) UntrackMint(mint string) {
	m.mu.Lock()
	existed := m.tracked[mint]
	delete(m.tracked, mint)
	m.mu.Unlock()
	if !existed {
		return
	}
	m.writeJSON(map[string]any{
		"method": "unsubscribeTokenTrade",
		"keys":   []string{mint},
	})
	m.log.Info("WS: dessubscrito de trades", zap.String("mint", mint))
}

func (m *Monitor) isTracked(mint string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tracked[mint]
}

func (m *Monitor) writeJSON(v any) error {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.conn == nil {
		return fmt.Errorf("ws not connected")
	}
	return m.conn.WriteJSON(v)
}

func (m *Monitor) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := m.connect(ctx); err != nil {
			m.log.Warn("PumpPortal WS disconnected",
				zap.Error(err),
				zap.Duration("retry_in", backoff),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (m *Monitor) connect(ctx context.Context) error {
	u, err := url.Parse(m.wsURL)
	if err != nil {
		return err
	}
	q := u.Query()
	if m.apiKey != "" {
		q.Set("api-key", m.apiKey)
	}
	u.RawQuery = q.Encode()

	c, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return err
	}

	m.connMu.Lock()
	m.conn = c
	m.connMu.Unlock()

	defer func() {
		m.connMu.Lock()
		m.conn = nil
		m.connMu.Unlock()
		c.Close()
	}()

	if err := c.WriteJSON(map[string]string{"method": "subscribeNewToken"}); err != nil {
		return err
	}
	m.log.Info("PumpPortal WS conectado; subscrito a subscribeNewToken")

	m.mu.Lock()
	var toResub []string
	for mint := range m.tracked {
		toResub = append(toResub, mint)
	}
	m.mu.Unlock()
	if len(toResub) > 0 {
		_ = c.WriteJSON(map[string]any{
			"method": "subscribeTokenTrade",
			"keys":   toResub,
		})
		m.log.Info("WS: re-subscrito a trades depois de reconectar", zap.Int("count", len(toResub)))
	}

	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return err
		}
		m.handleFrame(ctx, raw)
	}
}

type wsFrame struct {
	Mint                  string  `json:"mint"`
	Creator               string  `json:"creator"`          // NOVO: para eventos de criação
	TraderPublicKey       string  `json:"traderPublicKey"`  // Para eventos de trade
	TxType                string  `json:"txType"`
	Signature             string  `json:"signature"`
	MarketCapSol          float64 `json:"marketCapSol"`
	SolAmount             float64 `json:"solAmount"`
	CreatedTimestamp      float64 `json:"createdTimestamp"`
	Name                  string  `json:"name"`
	Symbol                string  `json:"symbol"`
	ImageURI              string  `json:"imageUri"`
	URI                   string  `json:"uri"`
	VSolInBondingCurve    float64 `json:"vSolInBondingCurve"`
	VTokensInBondingCurve float64 `json:"vTokensInBondingCurve"`
}

func (m *Monitor) handleFrame(ctx context.Context, raw []byte) {
	var p wsFrame
	if err := json.Unmarshal(raw, &p); err != nil {
		m.log.Warn("PumpPortal: frame inválido", zap.Error(err))
		return
	}
	if p.Mint == "" {
		return
	}

	// Fallback para a imagem
	img := p.ImageURI
	if img == "" {
		img = p.URI
	}

	// Fallback para o criador (create usa "creator", trade usa "traderPublicKey")
	creator := p.Creator
	if creator == "" {
		creator = p.TraderPublicKey
	}

	now := time.Now().UTC()

	if m.isTracked(p.Mint) {
		m.onTrackedTrade(p, now)
		return
	}

	if p.TxType == "buy" || p.TxType == "sell" {
		m.mom.Record(TradeEvent{
			Mint:      p.Mint,
			TxType:    p.TxType,
			SOLAmount: p.SolAmount,
			Trader:    p.TraderPublicKey,
			At:        now,
		})
	}

	if m.isDuplicate(p.Mint, now) {
		return
	}

	if p.MarketCapSol <= 0 {
		return
	}
	if m.cfg.MinMarketCapSOL > 0 && p.MarketCapSol < m.cfg.MinMarketCapSOL {
		return
	}
	if m.cfg.MaxMarketCapSOL > 0 && p.MarketCapSol > m.cfg.MaxMarketCapSOL {
		return
	}

	var tokenAge time.Duration
	if p.CreatedTimestamp > 0 {
		createdAt := time.Unix(int64(p.CreatedTimestamp), 0).UTC()
		tokenAge = now.Sub(createdAt)
	}
	if m.cfg.TokenMaxAge > 0 && tokenAge > m.cfg.TokenMaxAge {
		return
	}

	snap := m.mom.Evaluate(p.Mint, now)

	ctxProfile := ProfileContext{
		Mint:              p.Mint,
		TokenAge:          tokenAge,
		MarketCapSOL:      p.MarketCapSol,
		Momentum:          snap,
		MomentumEvaluated: snap.Evaluated,
	}

	decision := SelectProfile(m.profiles, ctxProfile)

	if !decision.Approved {
		_ = m.db.RecordAssessment(p.Mint, false, decision.Reason, "", 0)
		if m.bus != nil {
			m.bus.Publish("token_rejected", map[string]any{
				"mint":     p.Mint,
				"name":     p.Name,
				"symbol":   p.Symbol,
				"imageUri": img,
				"creator":  creator,
				"reason":   decision.Reason,
			})
		}
		return
	}

	_ = m.db.RecordAssessment(
		p.Mint, true, decision.Reason,
		string(decision.Profile), decision.Score,
	)

	// Grava com os metadados, liquidez e o creator correto
	_ = m.db.RecordEvent(
		p.Mint, creator, string(raw), now, 0,
		p.Name, p.Symbol, img, p.VSolInBondingCurve, p.VTokensInBondingCurve,
	)

	if m.cfg.EntryDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.cfg.EntryDelay):
		}
	}

	if m.buyFn != nil {
		if err := m.buyFn(
			ctx, p.Mint, creator, // Passa o creator correto
			p.MarketCapSol,
			string(decision.Profile), decision.Score, decision.Reason,
		); err != nil {
			m.log.Warn("buy falhou",
				zap.String("mint", p.Mint),
				zap.Error(err),
			)
		}
	}
}

func (m *Monitor) onTrackedTrade(p wsFrame, now time.Time) {
	if p.TxType == "buy" || p.TxType == "sell" {
		m.mom.Record(TradeEvent{
			Mint:      p.Mint,
			TxType:    p.TxType,
			SOLAmount: p.SolAmount,
			Trader:    p.TraderPublicKey,
			At:        now,
		})
	}

	if p.MarketCapSol <= 0 {
		return
	}

	if err := m.db.UpsertMark(m.simulation, p.Mint, p.MarketCapSol, false); err != nil {
		m.log.Warn("falha a atualizar mark",
			zap.String("mint", p.Mint),
			zap.Error(err),
		)
		return
	}

	if m.bus != nil {
		m.bus.Publish("price_update", map[string]any{
			"mint":     p.Mint,
			"mcapSOL":  p.MarketCapSol,
		})
	}
}

func (m *Monitor) isDuplicate(mint string, now time.Time) bool {
	ttl := m.cfg.DedupeTTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.seen[mint]; ok && now.Sub(t) < ttl {
		return true
	}
	m.seen[mint] = now
	return false
}

func (m *Monitor) Connected() bool {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	return m.conn != nil
}