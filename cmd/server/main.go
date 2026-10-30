package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"sniper-lightning/internal/api"
	"sniper-lightning/internal/config"
	"sniper-lightning/internal/database"
	"sniper-lightning/internal/events"
	"sniper-lightning/internal/execution"
	"sniper-lightning/internal/execution/pumpportal"
	"sniper-lightning/internal/positions"
	"sniper-lightning/internal/tokenmonitor"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config inválida", zap.Error(err))
	}

	// ---------- Guard de segurança do modo real ----------
	if !cfg.Simulation && !cfg.EnableLiveTrading {
		log.Fatal(
			"SIMULATION=false mas ENABLE_LIVE_TRADING=false — abortar para segurança. " +
				"Para operar a sério define ENABLE_LIVE_TRADING=true no .env.",
		)
	}
	if cfg.Simulation && cfg.EnableLiveTrading {
		log.Warn("SIMULATION=true com ENABLE_LIVE_TRADING=true — SIMULATION tem precedência; modo paper ativo")
	}
	log.Info("modo de arranque",
		zap.Bool("simulation", cfg.Simulation),
		zap.Bool("live_trading_enabled", cfg.EnableLiveTrading),
	)

	// ---------- Base de dados ----------
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatal("falha ao abrir base de dados", zap.Error(err))
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatal("falha na migração", zap.Error(err))
	}

	if cfg.Simulation {
		_ = db.EnsureSimulationSession(cfg.SimulationStartBalanceSOL)
	}

	// ---------- Event bus ----------
	bus := events.New()

	// ---------- PumpPortal client ----------
	portal := pumpportal.New(cfg.PumpPortalURL, cfg.PumpPortalKey)

	// ---------- Execution engine ----------
	engine := execution.New(
		db,
		portal,
		log,
		bus,
		cfg.Simulation,
		execution.Limits{
			MaxTradeSOL:          cfg.MaxTradeSOL,
			MaxExposureSOL:       cfg.MaxExposureSOL,
			MaxDailyLossSOL:      cfg.MaxDailyLossSOL,
			FeeReserveSOL:        cfg.FeeReserveSOL,
			MaxOpenPositions:     cfg.MaxOpenPositions,
			MaxSlippageBPS:       cfg.MaxSlippageBPS,
			MaxPriorityFeeSOL:    cfg.MaxPriorityFeeSOL,
			SimulationBaseFeeSOL: 0.0001,
		},
	)

	// ---------- Perfis de entrada ----------
	profiles := tokenmonitor.Profiles{
		Mode: tokenmonitor.EntryProfileMode(strings.ToLower(cfg.EntryProfileMode)),
		FirstPump: tokenmonitor.ProfileConfig{
			Enabled:               cfg.FirstPumpEnabled,
			MaxTokenAge:           time.Duration(cfg.FirstPumpMaxTokenAgeSecs) * time.Second,
			MinMarketCapSOL:       cfg.FirstPumpMinMarketCapSOL,
			MaxMarketCapSOL:       cfg.FirstPumpMaxMarketCapSOL,
			MinVolumeBeforeBuySOL: cfg.FirstPumpMinVolumeSOL,
			MaxVolumeBeforeBuySOL: cfg.FirstPumpMaxVolumeSOL,
			ScoreWeightVolume:     0.3,
			ScoreWeightBuyRatio:   0.3,
			ScoreWeightBuyers:     0.2,
			ScoreWeightAccel:      0.2,
			ScoreVolumeCapSOL:     10,
			ScoreBuyersCap:        20,
		},
		Momentum: tokenmonitor.ProfileConfig{
			Enabled:             cfg.MomentumProfileEnabled,
			MaxTokenAge:         time.Duration(cfg.MomentumProfileMaxAgeSecs) * time.Second,
			MinMarketCapSOL:     cfg.MomentumProfileMinMcapSOL,
			MaxMarketCapSOL:     cfg.MomentumProfileMaxMcapSOL,
			MinHolders:          cfg.MomentumProfileMinHolders,
			RequireMomentum:     cfg.MomentumProfileRequireMom,
			ScoreWeightVolume:   0.3,
			ScoreWeightBuyRatio: 0.3,
			ScoreWeightBuyers:   0.2,
			ScoreWeightAccel:    0.2,
			ScoreVolumeCapSOL:   20,
			ScoreBuyersCap:      30,
		},
	}
	if profiles.Mode == "" {
		profiles.Mode = tokenmonitor.ModeAuto
	}

	// ---------- Monitor ----------
	monitorCfg := tokenmonitor.MonitorConfig{
		EntryDelay:      time.Duration(cfg.EntryDelaySeconds) * time.Second,
		TokenMaxAge:     time.Duration(cfg.TokenMaxAgeSeconds) * time.Second,
		MinMarketCapSOL: cfg.MinMarketCapSOL,
		MaxMarketCapSOL: cfg.MaxMarketCapSOL,
		DedupeTTL:       time.Duration(cfg.DedupeTTLMS) * time.Millisecond,
		Momentum: tokenmonitor.MomentumConfig{
			ShortWindow:     time.Duration(cfg.MomentumShortWindowSeconds) * time.Second,
			MediumWindow:    time.Duration(cfg.MomentumMediumWindowSeconds) * time.Second,
			LongWindow:      time.Duration(cfg.MomentumLongWindowSeconds) * time.Second,
			MinVolumeSOL:    cfg.MomentumMinVolumeSOL,
			MinBuyRatio:     cfg.MomentumMinBuyRatio,
			MinUniqueBuyers: cfg.MomentumMinUniqueBuyers,
			MinAcceleration: cfg.MomentumMinAcceleration,
			MaxTradeAge:     time.Duration(cfg.MomentumMaxTradeAgeSeconds) * time.Second,
		},
	}

	// Callback de compra com creator (para blacklist) e mcap (para mark).
	buyFn := func(
		ctx context.Context,
		mint string,
		creator string,
		mcapSOL float64,
		profile string,
		score float64,
		reason string,
	) error {
		openCount, err := db.OpenPositionCount()
		if err != nil {
			return err
		}
		if cfg.MaxOpenPositions > 0 && openCount >= cfg.MaxOpenPositions {
			return nil
		}
		has, err := db.HasOpenPosition(mint)
		if err != nil {
			return err
		}
		if has {
			return nil
		}

		slippagePct := float64(cfg.MaxSlippageBPS) / 100.0
		priorityFee := cfg.MaxPriorityFeeSOL
		pool := "pump"

		_, err = engine.BuyWithMark(ctx, execution.BuyRequest{
			Mint:           mint,
			Creator:        creator,
			AmountSOL:      cfg.TradeUSD,
			MarketCapSOL:   mcapSOL,
			SlippagePct:    slippagePct,
			PriorityFeeSOL: priorityFee,
			Pool:           pool,
			Reason:         reason,
			Profile:        profile,
			Score:          score,
		})
		return err
	}

	monitor := tokenmonitor.New(
		cfg.PumpPortalWS,
		cfg.PumpPortalKey,
		db,
		bus,
		log,
		monitorCfg,
		profiles,
		cfg.Simulation,
		buyFn,
	)

	// ---------- Position Manager ----------
	exitLimits := positions.Limits{
		TakeProfitPct:        cfg.TakeProfitGrossPct,
		StopLossPct:          cfg.StopLossPct,
		TrailingStopPct:      cfg.TrailingStopPct,
		TrailingStopArmPct:   cfg.TrailingStopArmPct,
		ExitTriggerBufferPct: 0.5,
		MaxHold:              time.Duration(cfg.MaxHoldSeconds) * time.Second,
	}

	pollInterval := time.Duration(cfg.PricePollSeconds) * time.Second
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}

	manager := positions.NewManager(
		db,
		engine,  // satisfaz positions.Seller
		monitor, // satisfaz positions.PriceTracker
		log,
		positions.ManagerConfig{
			Simulation:     cfg.Simulation,
			PollInterval:   pollInterval,
			SlippagePct:    float64(cfg.MaxSlippageBPS) / 100.0,
			PriorityFeeSOL: cfg.MaxPriorityFeeSOL,
			Limits:         exitLimits,
		},
	)

	// ---------- Arranque ----------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go monitor.Run(ctx)
	go manager.Run(ctx)

	// ---------- HTTP API ----------
	handler := api.New(db, engine, monitor, bus, cfg, log)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("HTTP server a ouvir", zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("HTTP server falhou", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("a desligar...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	fmt.Println("servidor terminado")
}