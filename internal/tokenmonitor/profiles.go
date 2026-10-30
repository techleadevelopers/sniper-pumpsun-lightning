package tokenmonitor

import (
	"fmt"
	"time"
)

type EntryProfile string

const (
	ProfileFirstPump EntryProfile = "first_pump"
	ProfileMomentum  EntryProfile = "momentum"
)

type EntryProfileMode string

const (
	ModeAuto          EntryProfileMode = "auto"
	ModeFirstPumpOnly EntryProfileMode = "first_pump_only"
	ModeMomentumOnly  EntryProfileMode = "momentum_only"
)

type ProfileConfig struct {
	Enabled               bool
	MaxTokenAge           time.Duration
	MinMarketCapSOL       float64
	MaxMarketCapSOL       float64
	MinVolumeBeforeBuySOL float64
	MaxVolumeBeforeBuySOL float64
	MinHolders            int
	RequireMomentum       bool

	ScoreWeightVolume   float64
	ScoreWeightBuyRatio float64
	ScoreWeightBuyers   float64
	ScoreWeightAccel    float64
	ScoreVolumeCapSOL   float64
	ScoreBuyersCap      float64
}

type Profiles struct {
	Mode      EntryProfileMode
	FirstPump ProfileConfig
	Momentum  ProfileConfig
}

func DefaultProfiles() Profiles {
	return Profiles{
		Mode: ModeAuto,
		FirstPump: ProfileConfig{
			Enabled:               true,
			MaxTokenAge:           30 * time.Second,
			MaxMarketCapSOL:       4,
			MaxVolumeBeforeBuySOL: 5,
			ScoreWeightVolume:     0.3,
			ScoreWeightBuyRatio:   0.3,
			ScoreWeightBuyers:     0.2,
			ScoreWeightAccel:      0.2,
			ScoreVolumeCapSOL:     10,
			ScoreBuyersCap:        20,
		},
		Momentum: ProfileConfig{
			Enabled:             true,
			RequireMomentum:     true,
			ScoreWeightVolume:   0.3,
			ScoreWeightBuyRatio: 0.3,
			ScoreWeightBuyers:   0.2,
			ScoreWeightAccel:    0.2,
			ScoreVolumeCapSOL:   20,
			ScoreBuyersCap:      30,
		},
	}
}

type ProfileContext struct {
	Mint               string
	TokenAge           time.Duration
	MarketCapSOL       float64
	RealSOLReservesSOL float64
	Momentum           MomentumSnapshot
	MomentumEvaluated  bool
	HolderCount        int
	HolderCountKnown   bool
}

type ProfileDecision struct {
	Approved bool
	Profile  EntryProfile
	Score    float64
	Reason   string
}

func SelectProfile(p Profiles, ctx ProfileContext) ProfileDecision {
	// 1) First Pump
	if p.FirstPump.Enabled && (p.Mode == ModeAuto || p.Mode == ModeFirstPumpOnly) {
		if reason := checkFirstPump(p.FirstPump, ctx); reason == "" {
			return ProfileDecision{
				Approved: true,
				Profile:  ProfileFirstPump,
				Score:    scoreFor(p.FirstPump, ctx),
				Reason:   "first_pump matched",
			}
		}
	}

	// 2) Momentum
	if p.Momentum.Enabled && (p.Mode == ModeAuto || p.Mode == ModeMomentumOnly) {
		if reason := checkMomentum(p.Momentum, ctx); reason == "" {
			return ProfileDecision{
				Approved: true,
				Profile:  ProfileMomentum,
				Score:    scoreFor(p.Momentum, ctx),
				Reason:   "momentum matched",
			}
		}
	}

	return ProfileDecision{Approved: false, Reason: "no profile matched"}
}

func checkFirstPump(c ProfileConfig, ctx ProfileContext) string {
	if c.MaxTokenAge > 0 && ctx.TokenAge > c.MaxTokenAge {
		return "first_pump: token too old"
	}
	if ctx.MarketCapSOL < c.MinMarketCapSOL {
		return fmt.Sprintf("first_pump: mcap %.6f below min %.6f", ctx.MarketCapSOL, c.MinMarketCapSOL)
	}
	if c.MaxMarketCapSOL > 0 && ctx.MarketCapSOL > c.MaxMarketCapSOL {
		return fmt.Sprintf("first_pump: mcap %.6f exceeds max %.6f", ctx.MarketCapSOL, c.MaxMarketCapSOL)
	}
	if c.MinVolumeBeforeBuySOL > 0 && ctx.RealSOLReservesSOL < c.MinVolumeBeforeBuySOL {
		return "first_pump: reserves below min volume"
	}
	if c.MaxVolumeBeforeBuySOL > 0 && ctx.RealSOLReservesSOL > c.MaxVolumeBeforeBuySOL {
		return fmt.Sprintf("first_pump: real SOL reserves %.6f exceed max %.6f", ctx.RealSOLReservesSOL, c.MaxVolumeBeforeBuySOL)
	}
	return ""
}

func checkMomentum(c ProfileConfig, ctx ProfileContext) string {
	if c.MaxTokenAge > 0 && ctx.TokenAge > c.MaxTokenAge {
		return "momentum: token too old"
	}
	if ctx.MarketCapSOL < c.MinMarketCapSOL {
		return "momentum: mcap below min"
	}
	if c.MaxMarketCapSOL > 0 && ctx.MarketCapSOL > c.MaxMarketCapSOL {
		return "momentum: mcap above max"
	}
	if c.MinHolders > 0 && ctx.HolderCountKnown && ctx.HolderCount < c.MinHolders {
		return "momentum: insufficient holders"
	}
	if c.RequireMomentum {
		if !ctx.MomentumEvaluated {
			return "momentum: not evaluated"
		}
		if ctx.Momentum.VolumeLong < 0.01 {
			return "momentum: insufficient volume"
		}
	}
	return ""
}

func scoreFor(c ProfileConfig, ctx ProfileContext) float64 {
	var total, weight float64
	m := ctx.Momentum

	if c.ScoreVolumeCapSOL > 0 && c.ScoreWeightVolume > 0 {
		v := m.VolumeLong / c.ScoreVolumeCapSOL
		if v > 1 {
			v = 1
		}
		total += c.ScoreWeightVolume * v * 100
		weight += c.ScoreWeightVolume
	}

	if c.ScoreBuyersCap > 0 && c.ScoreWeightBuyers > 0 {
		v := float64(m.UniqueBuyers) / c.ScoreBuyersCap
		if v > 1 {
			v = 1
		}
		total += c.ScoreWeightBuyers * v * 100
		weight += c.ScoreWeightBuyers
	}

	if c.ScoreWeightBuyRatio > 0 {
		total += c.ScoreWeightBuyRatio * m.BuyRatio * 100
		weight += c.ScoreWeightBuyRatio
	}

	if c.ScoreWeightAccel > 0 && m.Acceleration > 0 {
		v := m.Acceleration
		if v > 2 {
			v = 2
		}
		total += c.ScoreWeightAccel * v * 50
		weight += c.ScoreWeightAccel
	}

	if weight <= 0 {
		return 0
	}
	return total / weight
}
