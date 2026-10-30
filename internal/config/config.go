package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Simulation        bool
	EnableLiveTrading bool
	DatabaseURL       string
	SimulationDBURL   string
	RealDBURL         string
	AdminPassword     string
	HTTPAddr          string
	PumpPortalKey     string
	PumpPortalURL     string
	PumpPortalWS      string
	WalletPublicAddr  string

	TradeUSD           float64
	MaxTradeSOL        float64
	MaxExposureSOL     float64
	MaxDailyLossSOL    float64
	FeeReserveSOL      float64
	MaxOpenPositions   int
	MaxSlippageBPS     int
	MaxPriorityFeeSOL  float64
	TakeProfitGrossPct float64
	StopLossPct        float64
	TrailingStopPct    float64
	TrailingStopArmPct float64
	MaxHoldSeconds     int

	TokenQueueSize     int
	EntryDelaySeconds  int
	SnipeConcurrency   int
	SnipeTimeoutMS     int
	DedupeTTLMS        int
	TokenMaxAgeSeconds int
	PricePollSeconds   int

	MinMarketCapSOL    float64
	MaxMarketCapSOL    float64
	MinVolumeBeforeBuy float64
	MaxVolumeBeforeBuy float64

	MomentumEnabled             bool
	MomentumShortWindowSeconds  int
	MomentumMediumWindowSeconds int
	MomentumLongWindowSeconds   int
	MomentumMinVolumeSOL        float64
	MomentumMinBuyRatio         float64
	MomentumMinUniqueBuyers     int
	MomentumMinAcceleration     float64
	MomentumMaxTradeAgeSeconds  int

	EntryProfileMode          string
	HoldersCacheSeconds       int
	FirstPumpEnabled          bool
	FirstPumpMaxTokenAgeSecs  int
	FirstPumpMinMarketCapSOL  float64
	FirstPumpMaxMarketCapSOL  float64
	FirstPumpMinVolumeSOL     float64
	FirstPumpMaxVolumeSOL     float64
	MomentumProfileEnabled    bool
	MomentumProfileMaxAgeSecs int
	MomentumProfileMinMcapSOL float64
	MomentumProfileMaxMcapSOL float64
	MomentumProfileMinVolSOL  float64
	MomentumProfileMaxVolSOL  float64
	MomentumProfileMinHolders int
	MomentumProfileRequireMom bool

	CORSAllowedOrigins string
}

func Load() (Config, error) {
	c := Config{}
	var err error

	c.Simulation, err = boolEnv("SIMULATION", true)
	if err != nil {
		return c, err
	}
	c.EnableLiveTrading, err = boolEnv("ENABLE_LIVE_TRADING", false)
	if err != nil {
		return c, err
	}

	c.SimulationDBURL = os.Getenv("SIMULATION_DATABASE_URL")
	c.RealDBURL = os.Getenv("REAL_DATABASE_URL")
	if c.SimulationDBURL == "" || c.RealDBURL == "" {
		return c, fmt.Errorf("SIMULATION_DATABASE_URL and REAL_DATABASE_URL are required")
	}
	if c.Simulation {
		c.DatabaseURL = c.SimulationDBURL
	} else {
		c.DatabaseURL = c.RealDBURL
	}

	c.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if c.AdminPassword == "" {
		return c, fmt.Errorf("ADMIN_PASSWORD is required")
	}

	c.HTTPAddr = env("HTTP_ADDR", ":8080")
	c.PumpPortalKey = os.Getenv("PUMPPORTAL_API_KEY")
	if c.PumpPortalKey == "" {
		return c, fmt.Errorf("PUMPPORTAL_API_KEY is required")
	}
	c.PumpPortalURL = env("PUMPPORTAL_URL", "https://pumpportal.fun/api/trade")
	c.PumpPortalWS = env("PUMPPORTAL_WS", "wss://pumpportal.fun/api/data")
	c.WalletPublicAddr = os.Getenv("WALLET_PUBLIC_ADDRESS")

	if c.TradeUSD, err = floatEnv("TRADE_USD", 1); err != nil {
		return c, err
	}
	if c.MaxTradeSOL, err = floatEnv("MAX_TRADE_SOL", 0.05); err != nil {
		return c, err
	}
	if c.MaxExposureSOL, err = floatEnv("MAX_EXPOSURE_SOL", 0.5); err != nil {
		return c, err
	}
	if c.MaxDailyLossSOL, err = floatEnv("MAX_DAILY_LOSS_SOL", 0.15); err != nil {
		return c, err
	}
	if c.FeeReserveSOL, err = floatEnv("FEE_RESERVE_SOL", 0.01); err != nil {
		return c, err
	}
	if c.MaxOpenPositions, err = intEnv("MAX_OPEN_POSITIONS", 10); err != nil {
		return c, err
	}
	if c.MaxSlippageBPS, err = intEnv("MAX_SLIPPAGE_BPS", 1500); err != nil {
		return c, err
	}
	if c.MaxPriorityFeeSOL, err = floatEnv("MAX_PRIORITY_FEE_SOL", 0.001); err != nil {
		return c, err
	}
	if c.TakeProfitGrossPct, err = floatEnv("TAKE_PROFIT_GROSS_PCT", 80); err != nil {
		return c, err
	}
	if c.StopLossPct, err = floatEnv("STOP_LOSS_PCT", 20); err != nil {
		return c, err
	}
	if c.TrailingStopPct, err = floatEnv("TRAILING_STOP_PCT", 15); err != nil {
		return c, err
	}
	if c.TrailingStopArmPct, err = floatEnv("TRAILING_STOP_ARM_PCT", 10); err != nil {
		return c, err
	}
	if c.MaxHoldSeconds, err = intEnv("MAX_HOLD_SECONDS", 1200); err != nil {
		return c, err
	}

	if c.TokenQueueSize, err = intEnv("TOKEN_QUEUE_SIZE", 5000); err != nil {
		return c, err
	}
	if c.EntryDelaySeconds, err = intEnv("ENTRY_DELAY_SECONDS", 1); err != nil {
		return c, err
	}
	if c.SnipeConcurrency, err = intEnv("SNIPE_CONCURRENCY", 4); err != nil {
		return c, err
	}
	if c.SnipeTimeoutMS, err = intEnv("SNIPE_TIMEOUT_MS", 15000); err != nil {
		return c, err
	}
	if c.DedupeTTLMS, err = intEnv("DEDUPE_TTL_MS", 60000); err != nil {
		return c, err
	}
	if c.TokenMaxAgeSeconds, err = intEnv("TOKEN_MAX_AGE_SECONDS", 60); err != nil {
		return c, err
	}
	if c.PricePollSeconds, err = intEnv("PRICE_POLL_SECONDS", 20); err != nil {
		return c, err
	}

	if c.MinMarketCapSOL, err = floatEnv("MIN_MARKET_CAP_SOL", 0); err != nil {
		return c, err
	}
	if c.MaxMarketCapSOL, err = floatEnv("MAX_MARKET_CAP_SOL", 0); err != nil {
		return c, err
	}
	if c.MinVolumeBeforeBuy, err = floatEnv("MIN_VOLUME_BEFORE_BUY", 0); err != nil {
		return c, err
	}
	if c.MaxVolumeBeforeBuy, err = floatEnv("MAX_VOLUME_BEFORE_BUY", 100000); err != nil {
		return c, err
	}

	if c.MomentumEnabled, err = boolEnv("MOMENTUM_ENABLED", true); err != nil {
		return c, err
	}
	if c.MomentumShortWindowSeconds, err = intEnv("MOMENTUM_SHORT_WINDOW_SECONDS", 10); err != nil {
		return c, err
	}
	if c.MomentumMediumWindowSeconds, err = intEnv("MOMENTUM_MEDIUM_WINDOW_SECONDS", 30); err != nil {
		return c, err
	}
	if c.MomentumLongWindowSeconds, err = intEnv("MOMENTUM_LONG_WINDOW_SECONDS", 60); err != nil {
		return c, err
	}
	if c.MomentumMinVolumeSOL, err = floatEnv("MOMENTUM_MIN_VOLUME_SOL", 0.05); err != nil {
		return c, err
	}
	if c.MomentumMinBuyRatio, err = floatEnv("MOMENTUM_MIN_BUY_RATIO", 0.60); err != nil {
		return c, err
	}
	if c.MomentumMinUniqueBuyers, err = intEnv("MOMENTUM_MIN_UNIQUE_BUYERS", 3); err != nil {
		return c, err
	}
	if c.MomentumMinAcceleration, err = floatEnv("MOMENTUM_MIN_ACCELERATION", 0.5); err != nil {
		return c, err
	}
	if c.MomentumMaxTradeAgeSeconds, err = intEnv("MOMENTUM_MAX_TRADE_AGE_SECONDS", 120); err != nil {
		return c, err
	}

	c.EntryProfileMode = strings.ToLower(env("ENTRY_PROFILE_MODE", "auto"))
	if c.HoldersCacheSeconds, err = intEnv("HOLDERS_CACHE_SECONDS", 30); err != nil {
		return c, err
	}
	if c.FirstPumpEnabled, err = boolEnv("FIRST_PUMP_ENABLED", true); err != nil {
		return c, err
	}
	if c.FirstPumpMaxTokenAgeSecs, err = intEnv("FIRST_PUMP_MAX_TOKEN_AGE_SECONDS", 30); err != nil {
		return c, err
	}
	if c.FirstPumpMinMarketCapSOL, err = floatEnv("FIRST_PUMP_MIN_MARKET_CAP_SOL", 0); err != nil {
		return c, err
	}
	if c.FirstPumpMaxMarketCapSOL, err = floatEnv("FIRST_PUMP_MAX_MARKET_CAP_SOL", 4); err != nil {
		return c, err
	}
	if c.FirstPumpMinVolumeSOL, err = floatEnv("FIRST_PUMP_MIN_VOLUME_SOL", 0); err != nil {
		return c, err
	}
	if c.FirstPumpMaxVolumeSOL, err = floatEnv("FIRST_PUMP_MAX_VOLUME_SOL", 5); err != nil {
		return c, err
	}
	if c.MomentumProfileEnabled, err = boolEnv("MOMENTUM_PROFILE_ENABLED", true); err != nil {
		return c, err
	}
	if c.MomentumProfileMaxAgeSecs, err = intEnv("MOMENTUM_PROFILE_MAX_TOKEN_AGE_SECONDS", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileMinMcapSOL, err = floatEnv("MOMENTUM_PROFILE_MIN_MARKET_CAP_SOL", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileMaxMcapSOL, err = floatEnv("MOMENTUM_PROFILE_MAX_MARKET_CAP_SOL", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileMinVolSOL, err = floatEnv("MOMENTUM_PROFILE_MIN_VOLUME_SOL", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileMaxVolSOL, err = floatEnv("MOMENTUM_PROFILE_MAX_VOLUME_SOL", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileMinHolders, err = intEnv("MOMENTUM_PROFILE_MIN_HOLDERS", 0); err != nil {
		return c, err
	}
	if c.MomentumProfileRequireMom, err = boolEnv("MOMENTUM_PROFILE_REQUIRE_MOMENTUM", true); err != nil {
		return c, err
	}

	c.CORSAllowedOrigins = os.Getenv("CORS_ALLOWED_ORIGINS")

	return c, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func boolEnv(k string, d bool) (bool, error) {
	v := strings.TrimSpace(env(k, strconv.FormatBool(d)))
	return strconv.ParseBool(strings.Trim(v, `"'`))
}
func floatEnv(k string, d float64) (float64, error) {
	v := strings.TrimSpace(env(k, strconv.FormatFloat(d, 'f', -1, 64)))
	return strconv.ParseFloat(strings.Trim(v, `"'`), 64)
}
func intEnv(k string, d int) (int, error) {
	v := strings.TrimSpace(env(k, strconv.Itoa(d)))
	return strconv.Atoi(strings.Trim(v, `"'`))
}