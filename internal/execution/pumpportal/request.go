package pumpportal

// Request representa o corpo aceite pela Lightning Transaction API.
// https://pumpportal.fun/trading-api/
type Request struct {
	Action           string  `json:"action"`           // "buy" ou "sell"
	Mint             string  `json:"mint"`             // contract address
	Amount           any     `json:"amount"`           // SOL (número) ou tokens / "100%"
	DenominatedInSOL string  `json:"denominatedInSol"` // "true" = SOL, "false" = tokens
	Slippage         float64 `json:"slippage"`         // percentagem (ex.: 10)
	PriorityFee      float64 `json:"priorityFee"`      // SOL
	Pool             string  `json:"pool,omitempty"`   // pump | pump-amm | auto | ...
	SkipPreflight    bool    `json:"skipPreflight"`    // default da API = true
	JitoOnly         bool    `json:"jitoOnly"`         // default = false
}

// Response é a resposta JSON devolvida pela Lightning API.
// Em caso de sucesso contém a assinatura; em caso de erro pode conter "error".
type Response struct {
	Signature string `json:"signature"`
	Error     string `json:"error"`
}
