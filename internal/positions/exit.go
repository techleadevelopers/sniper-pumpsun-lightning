package positions

import "time"

const (
	maxHoldProfitExitPct = 5.0
	maxHoldLossExitPct   = -10.0
)

type Position struct {
	Mint     string
	CostSOL  float64
	PeakSOL  float64
	OpenedAt time.Time
}

type Limits struct {
	TakeProfitPct        float64
	StopLossPct          float64
	TrailingStopPct      float64
	TrailingStopArmPct   float64
	ExitTriggerBufferPct float64
	MaxHold              time.Duration
}

// ExitReason decide a saída apenas com preço verificado.
// Ordem (contrato da máquina de estados):
//  1. sem preço verificado / sem custo   -> não decide
//  2. stop loss                          -> única razão que dispara blacklist
//  3. trailing stop                      -> só depois do braço armado
//  4. take profit
//  5. max hold                           -> liberta vagas, exclui a dead zone
func ExitReason(p Position, currentSOL float64, liquid bool, l Limits, now time.Time) string {
	if !liquid {
		return ""
	}
	if p.CostSOL <= 0 {
		return ""
	}
	change := (currentSOL - p.CostSOL) / p.CostSOL * 100

	// 1) stop loss primeiro — o reason exacto "stop loss" é o que a
	//    creator_blacklist observa.
	if l.StopLossPct > 0 && change <= -l.StopLossPct+l.ExitTriggerBufferPct {
		return "stop loss"
	}

	// 2) trailing stop (só se o pico já armou o braço)
	if l.TrailingStopPct > 0 && p.PeakSOL > p.CostSOL && currentSOL > 0 {
		peakProfitPct := (p.PeakSOL - p.CostSOL) / p.CostSOL * 100
		if peakProfitPct >= l.TrailingStopArmPct {
			drawdownFromPeak := (p.PeakSOL - currentSOL) / p.PeakSOL * 100
			if drawdownFromPeak >= l.TrailingStopPct-l.ExitTriggerBufferPct {
				return "trailing stop"
			}
		}
	}

	// 3) take profit
	if l.TakeProfitPct > 0 && change >= l.TakeProfitPct-l.ExitTriggerBufferPct {
		return "take profit"
	}

	// 4) max hold — liberta vagas; a dead zone (-10%, +5%) fica aberta
	if l.MaxHold > 0 && now.Sub(p.OpenedAt) >= l.MaxHold {
		switch {
		case change == 0:
			return "max hold flat (liberar vaga)"
		case change >= maxHoldProfitExitPct:
			return "max hold (gain threshold)"
		case change <= maxHoldLossExitPct:
			return "max hold (loss threshold)"
		}
	}
	return ""
}

// IsStopLoss devolve true quando o reason é exactamente "stop loss".
// Usado para acionar creator_blacklist.
func IsStopLoss(reason string) bool { return reason == "stop loss" }
