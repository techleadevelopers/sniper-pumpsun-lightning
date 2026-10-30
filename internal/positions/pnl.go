package positions

import "math"

func PaperPnLSOL(entryMcap, currentMcap, costSOL, exitFeeSOL float64) (valueSOL, pnlSOL float64, ok bool) {
	if entryMcap <= 0 || currentMcap <= 0 || costSOL <= 0 {
		return 0, 0, false
	}
	if math.IsNaN(entryMcap) || math.IsInf(entryMcap, 0) ||
		math.IsNaN(currentMcap) || math.IsInf(currentMcap, 0) {
		return 0, 0, false
	}

	ratio := currentMcap / entryMcap
	valueSOL = costSOL * ratio

	lightningFee := valueSOL * 0.01 // 1% PumpPortal
	pnlSOL = valueSOL - costSOL - lightningFee - exitFeeSOL
	return valueSOL, pnlSOL, true
}
