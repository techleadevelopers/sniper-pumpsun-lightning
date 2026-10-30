package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"sniper-lightning/internal/config"
	"sniper-lightning/internal/database"
	"sniper-lightning/internal/events"
	"sniper-lightning/internal/execution"
	"sniper-lightning/internal/tokenmonitor"
)

type Server struct {
	db      *database.DB
	engine  *execution.Engine
	monitor *tokenmonitor.Monitor
	bus     *events.Bus
	cfg     config.Config
	log     *zap.Logger
}

func New(
	db *database.DB,
	engine *execution.Engine,
	monitor *tokenmonitor.Monitor,
	bus *events.Bus,
	cfg config.Config,
	log *zap.Logger,
) http.Handler {
	s := &Server{db: db, engine: engine, monitor: monitor, bus: bus, cfg: cfg, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/auth/login", s.login)
	mux.HandleFunc("/api/dashboard", s.admin(s.dashboard))
	mux.HandleFunc("/api/tokens", s.admin(s.tokens))
	mux.HandleFunc("/api/trades", s.admin(s.trades))
	mux.HandleFunc("/ws/events", s.ws)

	return corsPolicy(mux, cfg.CORSAllowedOrigins)
}

// ---------- helpers ----------

func method(w http.ResponseWriter, r *http.Request, m string) bool {
	if r.Method != m {
		fail(w, 405, "method_not_allowed", "method not allowed")
		return false
	}
	return true
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]any{"error": code, "message": msg})
}

// ---------- auth ----------

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if !s.validToken(token) {
			fail(w, 401, "unauthorized", "invalid or missing token")
			return
		}
		next(w, r)
	}
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("sniper_auth"); err == nil {
		return c.Value
	}
	return ""
}

// validToken espera um token no formato "<exp>.<hmac>".
func (s *Server) validToken(token string) bool {
	// O cookie vai em base64 URL-safe; o header Authorization vai em claro.
	if decoded, err := base64.RawURLEncoding.DecodeString(token); err == nil {
		token = string(decoded)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expStr, sig := parts[0], parts[1]

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}

	mac := hmac.New(sha256.New, []byte(s.cfg.AdminPassword))
	mac.Write([]byte(expStr))
	expected := hex.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) == 1
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "bad_request", "invalid JSON")
		return
	}

	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.cfg.AdminPassword)) != 1 {
		fail(w, 401, "unauthorized", "invalid password")
		return
	}

	exp := time.Now().Add(24 * time.Hour).Unix()
	expStr := strconv.FormatInt(exp, 10)

	mac := hmac.New(sha256.New, []byte(s.cfg.AdminPassword))
	mac.Write([]byte(expStr))
	sig := hex.EncodeToString(mac.Sum(nil))

	raw := expStr + "." + sig
	token := base64.RawURLEncoding.EncodeToString([]byte(raw))

	http.SetCookie(w, &http.Cookie{
		Name:     "sniper_auth",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
	})

	write(w, 200, map[string]any{"token": token, "expiresAt": exp})
}

// ---------- handlers ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	state := "disconnected"
	if s.monitor != nil && s.monitor.Connected() {
		state = "connected"
	}
	write(w, 200, map[string]any{
		"status":             "ok",
		"eventMonitorStatus": state,
		"checkedAt":          time.Now().UTC(),
	})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}

	positions, err := s.db.OpenPositionCount()
	if err != nil {
		fail(w, 500, "database_unavailable", "open position count: "+err.Error())
		return
	}

	detected, _ := s.db.TokenEventCount()
	analyzed, _ := s.db.RiskAssessmentCount()
	tradesCount, _ := s.db.TradeCount()

	// NOVO: Buscar o PnL realizado (apenas vendas simuladas)
	var realizedPnl float64
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(realized_pnl_sol), 0) FROM trades WHERE state='simulated' AND side='sell'`).Scan(&realizedPnl)

	mode := "LIVE"
	if s.cfg.Simulation {
		mode = "SIMULATION"
	}

	write(w, 200, map[string]any{
		"mode":           mode,
		"openPositions":  positions,
		"detectedTokens": detected,
		"analyzedTokens": analyzed,
		"tradesCount":    tradesCount,
		"realizedPnlSOL": realizedPnl, // NOVO
	})
}

func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}

	rows, err := s.db.ListTokens()
	if err != nil {
		fail(w, 500, "database_unavailable", err.Error())
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 64)

	for rows.Next() {
		var (
			mint, creator, reason, entryProfile string
			side, tradeState, positionState     string
			latency                             int64
			approved                            sql.NullBool
			entryScore                          sql.NullFloat64
			receivedAt                          time.Time
		)

		if err := rows.Scan(
			&mint, &creator, &receivedAt, &latency,
			&approved, &reason, &entryProfile, &entryScore,
			&side, &tradeState, &positionState,
		); err != nil {
			s.log.Warn("tokens: scan falhou", zap.Error(err))
			continue
		}

		out = append(out, map[string]any{
			"mint":          mint,
			"creator":       creator,
			"receivedAt":    receivedAt.UTC().Format(time.RFC3339),
			"latencyMs":     latency,
			"approved":      approved.Valid && approved.Bool,
			"reason":        reason,
			"entryProfile":  entryProfile,
			"entryScore":    entryScore.Float64,
			"tradeSide":     side,
			"tradeState":    tradeState,
			"positionState": positionState,
		})
	}

	if err := rows.Err(); err != nil {
		s.log.Warn("tokens: rows err", zap.Error(err))
	}

	write(w, 200, map[string]any{"tokens": out})
}

func (s *Server) trades(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}

	rows, err := s.db.ListTrades()
	if err != nil {
		fail(w, 500, "database_unavailable", err.Error())
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 64)

	for rows.Next() {
		var (
			id, mint, side, quantity     string
			requested, executed, fees    float64
			slippage                     float64
			signature, state, exitReason string
			entryProfile                 string
			entryScore                   float64
			createdAt                    time.Time
			realizedPnl                  sql.NullFloat64
		)

		if err := rows.Scan(
			&id, &mint, &side, &quantity,
			&requested, &executed, &fees, &slippage,
			&signature, &state, &createdAt,
			&realizedPnl, &exitReason, &entryProfile, &entryScore,
		); err != nil {
			s.log.Warn("trades: scan falhou", zap.Error(err))
			continue
		}

		out = append(out, map[string]any{
			"id":                id,
			"mint":              mint,
			"side":              side,
			"quantity":          quantity,
			"requestedValueSOL": requested,
			"executedValueSOL":  executed,
			"feesSOL":           fees,
			"slippagePct":       slippage,
			"signature":         signature,
			"state":             state,
			"createdAt":         createdAt.UTC().Format(time.RFC3339),
			"realizedPnlSOL":    realizedPnl.Float64,
			"exitReason":        exitReason,
			"entryProfile":      entryProfile,
			"entryScore":        entryScore,
		})
	}

	if err := rows.Err(); err != nil {
		s.log.Warn("trades: rows err", zap.Error(err))
	}

	write(w, 200, map[string]any{"trades": out})
}

// ---------- WebSocket ----------

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	if s.bus == nil {
		fail(w, 503, "bus_unavailable", "event bus not configured")
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// Em produção, restringe à origem do dashboard.
			return true
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("ws upgrade falhou", zap.Error(err))
		return
	}
	defer conn.Close()

	ch, cancel := s.bus.Subscribe()
	defer cancel()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// ---------- CORS ----------

func corsPolicy(next http.Handler, allowedOrigins string) http.Handler {
	allowed := map[string]struct{}{}
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}