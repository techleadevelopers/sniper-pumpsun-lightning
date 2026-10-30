package execution

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"sniper-lightning/internal/database"
	"sniper-lightning/internal/events"
	"sniper-lightning/internal/execution/pumpportal"
	"sniper-lightning/internal/positions"
)

type Limits struct {
	MaxTradeSOL          float64
	MaxExposureSOL       float64
	MaxDailyLossSOL      float64
	FeeReserveSOL        float64
	MaxOpenPositions     int
	MaxSlippageBPS       int
	MaxPriorityFeeSOL    float64
	SimulationBaseFeeSOL float64 // fee fictícia de rede em simulação
}

type Engine struct {
	db         *database.DB
	portal     *pumpportal.Client
	log        *zap.Logger
	bus        *events.Bus
	simulation bool

	mu       sync.Mutex
	limits   Limits
	reserved map[string]float64
}

func New(
	db *database.DB,
	portal *pumpportal.Client,
	log *zap.Logger,
	bus *events.Bus,
	simulation bool,
	limits Limits,
) *Engine {
	return &Engine{
		db:         db,
		portal:     portal,
		log:        log,
		bus:        bus,
		simulation: simulation,
		limits:     limits,
		reserved:   make(map[string]float64),
	}
}

func (e *Engine) Limits() Limits {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.limits
}

func (e *Engine) SetLimits(l Limits) {
	e.mu.Lock()
	e.limits = l
	e.mu.Unlock()
}

func (e *Engine) simBaseFeeSOL() float64 {
	if v := e.Limits().SimulationBaseFeeSOL; v > 0 {
		return v
	}
	return 0.0001 // fallback: fee fictícia de rede
}

// ---------------------------------------------------------------------------
// Buy (assinatura antiga — mantida para compatibilidade com main.go)
// ---------------------------------------------------------------------------

func (e *Engine) Buy(
	ctx context.Context,
	mint string,
	amountSOL float64,
	slippagePct float64,
	priorityFeeSOL float64,
	pool string,
	reason string,
	profile string,
	score float64,
) (string, error) {

	if amountSOL <= 0 {
		return "", fmt.Errorf("buy: amount must be positive")
	}

	l := e.Limits()
	if l.MaxTradeSOL > 0 && amountSOL > l.MaxTradeSOL {
		return "", fmt.Errorf("buy: max trade SOL exceeded (%.6f > %.6f)", amountSOL, l.MaxTradeSOL)
	}

	e.mu.Lock()
	if _, exists := e.reserved[mint]; exists {
		e.mu.Unlock()
		return "", fmt.Errorf("buy: mint already reserved")
	}
	e.reserved[mint] = amountSOL
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.reserved, mint)
		e.mu.Unlock()
	}()

	id := fmt.Sprintf("tr_%d", time.Now().UnixNano())

	if err := e.db.InsertTrade(
		id, mint, "buy", fmt.Sprintf("%.9f", amountSOL),
		amountSOL, 0, slippagePct, reason, profile, score,
	); err != nil {
		return id, err
	}

	if e.simulation {
		if err := e.db.InsertPosition(mint, fmt.Sprintf("%.9f", amountSOL), amountSOL); err != nil {
			return id, err
		}
		if err := e.db.UpdateTradeState(id, "simulated", "", true); err != nil {
			return id, err
		}
		if e.bus != nil {
			e.bus.Publish("buy_confirmed", map[string]any{
				"tradeId": id, "mint": mint, "simulation": true,
			})
		}
		return id, nil
	}

	sig, err := e.portal.Buy(ctx, mint, amountSOL, slippagePct, priorityFeeSOL, pool)
	if err != nil {
		_ = e.db.UpdateTradeState(id, "failed", "", false)
		return id, err
	}
	_ = e.db.UpdateTradeState(id, "submitted", sig, false)
	if e.bus != nil {
		e.bus.Publish("buy_sent", map[string]any{
			"tradeId": id, "mint": mint, "signature": sig,
		})
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// BuyWithMark — versão usada pelo monitor, com mcap e creator
// ---------------------------------------------------------------------------

type BuyRequest struct {
	Mint           string
	Creator        string
	AmountSOL      float64
	MarketCapSOL   float64
	SlippagePct    float64
	PriorityFeeSOL float64
	Pool           string
	Reason         string
	Profile        string
	Score          float64
}

func (e *Engine) BuyWithMark(ctx context.Context, req BuyRequest) (string, error) {
	if req.AmountSOL <= 0 {
		return "", fmt.Errorf("buy: amount must be positive")
	}
	if req.MarketCapSOL <= 0 {
		return "", fmt.Errorf("buy: market cap must be known")
	}

	l := e.Limits()
	if l.MaxTradeSOL > 0 && req.AmountSOL > l.MaxTradeSOL {
		return "", fmt.Errorf("buy: max trade SOL exceeded")
	}

	// Uma compra por mint, para sempre.
	if bought, err := e.db.HasPurchaseForMint(req.Mint); err != nil {
		return "", err
	} else if bought {
		return "", fmt.Errorf("buy: mint already has a purchase")
	}

	// Blacklist de creator.
	if req.Creator != "" {
		if blocked, err := e.db.IsCreatorBlacklisted(req.Creator); err == nil && blocked {
			return "", fmt.Errorf("buy: creator is blacklisted")
		}
	}

	e.mu.Lock()
	if _, exists := e.reserved[req.Mint]; exists {
		e.mu.Unlock()
		return "", fmt.Errorf("buy: mint already reserved")
	}
	e.reserved[req.Mint] = req.AmountSOL
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.reserved, req.Mint)
		e.mu.Unlock()
	}()

	id := fmt.Sprintf("tr_%d", time.Now().UnixNano())

	simulationFees := 0.0
	if e.simulation {
		simulationFees = req.PriorityFeeSOL + e.simBaseFeeSOL()
	}

	if err := e.db.InsertTrade(
		id, req.Mint, "buy", fmt.Sprintf("%.9f", req.AmountSOL),
		req.AmountSOL, simulationFees, req.SlippagePct,
		req.Reason, req.Profile, req.Score,
	); err != nil {
		return id, err
	}

	if e.simulation {
		if err := e.db.InsertPositionWithFees(
			req.Mint, req.Creator, fmt.Sprintf("%.9f", req.AmountSOL),
			req.AmountSOL, simulationFees,
		); err != nil {
			return id, err
		}
		if err := e.db.UpsertMark(true, req.Mint, req.MarketCapSOL, true); err != nil {
			return id, err
		}
		if err := e.db.UpdateTradeState(id, "simulated", "", true); err != nil {
			return id, err
		}
		if e.bus != nil {
			e.bus.Publish("buy_confirmed", map[string]any{
				"tradeId": id, "mint": req.Mint, "simulation": true,
				"profile": req.Profile, "score": req.Score,
			})
		}
		return id, nil
	}

	// Modo real — Lightning API
	sig, err := e.portal.Buy(ctx, req.Mint, req.AmountSOL, req.SlippagePct, req.PriorityFeeSOL, req.Pool)
	if err != nil {
		_ = e.db.UpdateTradeState(id, "failed", "", false)
		return id, err
	}

	_ = e.db.InsertPositionWithFees(
		req.Mint, req.Creator, fmt.Sprintf("%.9f", req.AmountSOL),
		req.AmountSOL, simulationFees,
	)
	_ = e.db.UpsertMark(false, req.Mint, req.MarketCapSOL, true)
	_ = e.db.UpdateTradeState(id, "submitted", sig, false)

	if e.bus != nil {
		e.bus.Publish("buy_sent", map[string]any{
			"tradeId": id, "mint": req.Mint, "signature": sig,
		})
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// Sell — simulação realista com PnL
// ---------------------------------------------------------------------------

func (e *Engine) Sell(
	ctx context.Context,
	mint string,
	slippagePct float64,
	priorityFeeSOL float64,
	reason string,
) (string, error) {

	cost, err := e.db.PositionCost(mint)
	if err != nil {
		return "", fmt.Errorf("sell: no open position for mint %s: %w", mint, err)
	}

	id := fmt.Sprintf("tr_%d", time.Now().UnixNano())
	if err := e.db.InsertTrade(
		id, mint, "sell", "100%", cost, 0, slippagePct,
		reason, "", 0,
	); err != nil {
		return id, err
	}

	if e.simulation {
		// --- Simulação realista: usa marks + PnL real ---
		entryMcap, currentMcap, _, ok, markErr := e.db.Mark(true, mint)
		if markErr != nil {
			return id, fmt.Errorf("sell: read mark failed: %w", markErr)
		}
		if !ok {
			return id, fmt.Errorf("sell: no verified mark for mint %s (WS update required)", mint)
		}

		entryFees, _ := e.db.PositionFeesSOL(mint)
		exitFee := priorityFeeSOL + e.simBaseFeeSOL()

		valueSOL, pnlSOL, valid := positions.PaperPnLSOL(entryMcap, currentMcap, cost, exitFee)
		if !valid {
			return id, fmt.Errorf("sell: cannot compute paper PnL (invalid marks)")
		}
		totalFees := entryFees + exitFee

		if err := e.db.ClosePosition(mint); err != nil {
			return id, err
		}
		if err := e.db.RecordSimulatedSellPnl(id, mint, valueSOL, totalFees, pnlSOL, reason); err != nil {
			return id, err
		}
		_ = e.db.DeleteMark(true, mint)

		// Blacklist no stop loss.
		if positions.IsStopLoss(reason) {
			if creator, cerr := e.db.PositionCreator(mint); cerr == nil && creator != "" {
				_ = e.db.BlacklistCreator(creator, reason)
			}
		}

		if e.bus != nil {
			e.bus.Publish("sell_confirmed", map[string]any{
				"tradeId": id, "mint": mint, "simulation": true,
				"valueSOL": valueSOL, "pnlSOL": pnlSOL,
			})
		}
		return id, nil
	}

	// --- Modo real ---
	sig, err := e.portal.Sell(ctx, mint, "100%", slippagePct, priorityFeeSOL, "auto")
	if err != nil {
		_ = e.db.UpdateTradeState(id, "failed", "", false)
		return id, err
	}

	_ = e.db.ClosePosition(mint)
	_ = e.db.DeleteMark(false, mint)
	_ = e.db.UpdateTradeState(id, "submitted", sig, false)

	if e.bus != nil {
		e.bus.Publish("sell_sent", map[string]any{
			"tradeId": id, "mint": mint, "signature": sig,
		})
	}
	return id, nil
}