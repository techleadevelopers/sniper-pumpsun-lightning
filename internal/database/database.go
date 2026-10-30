package database

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/postgres/*.sql
var migrations embed.FS

type DB struct {
	*sql.DB
	mu sync.Mutex
}

// Open abre a ligação ao PostgreSQL e valida com Ping.
func Open(databaseURL string) (*DB, error) {
	if !strings.HasPrefix(databaseURL, "postgres://") && !strings.HasPrefix(databaseURL, "postgresql://") {
		return nil, fmt.Errorf("database URL must be postgres:// or postgresql://")
	}
	d, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		_ = d.Close()
		return nil, err
	}
	return &DB{DB: d}, nil
}

// Migrate aplica todas as migrações embebidas que ainda não foram aplicadas.
func (d *DB) Migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/postgres/*.sql")
	if err != nil {
		return err
	}
	for _, f := range files {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(f), "%d_", &version); err != nil {
			return fmt.Errorf("invalid migration filename %s: %w", f, err)
		}

		var exists int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=$1`, version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}

		body, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}

		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", f, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, applied_at) VALUES($1, $2)`,
			version, time.Now().UTC(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Trades
// ---------------------------------------------------------------------------

func (d *DB) InsertTrade(
	id, mint, side, quantity string,
	requested, fees, slippage float64,
	reason, profile string,
	score float64,
) error {
	now := time.Now().UTC()
	_, err := d.Exec(
		`INSERT INTO trades(
			id, mint, side, quantity,
			requested_value_sol, fees_sol, slippage_pct,
			state, entry_reason, entry_profile, entry_score,
			created_at, updated_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,'prepared',$8,$9,$10,$11,$12)`,
		id, mint, side, quantity,
		requested, fees, slippage,
		reason, profile, score,
		now, now,
	)
	return err
}

func (d *DB) UpdateTradeState(id, state, signature string, confirmed bool) error {
	now := time.Now().UTC()
	_, err := d.Exec(
		`UPDATE trades
		 SET state=$1,
		     signature=COALESCE(NULLIF($2,''), signature),
		     reconciliation_needed=$3,
		     updated_at=$4
		 WHERE id=$5`,
		state, signature, !confirmed, now, id,
	)
	return err
}

func (d *DB) ListTrades() (*sql.Rows, error) {
	return d.Query(
		`SELECT id, mint, side, quantity,
		        requested_value_sol,
		        COALESCE(executed_value_sol,0),
		        COALESCE(fees_sol,0),
		        slippage_pct,
		        COALESCE(signature,''),
		        state,
		        created_at,
		        realized_pnl_sol,
		        COALESCE(exit_reason,''),
		        COALESCE(entry_profile,''),
		        COALESCE(entry_score,0)
		 FROM trades
		 ORDER BY created_at DESC
		 LIMIT 200`,
	)
}

func (d *DB) TradeCount() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM trades`).Scan(&n)
	return n, err
}

// HasPurchaseForMint devolve true se já houve *qualquer* trade de compra
// (simulado, submetido, confirmado) para este mint. Bloqueia reentradas.
func (d *DB) HasPurchaseForMint(mint string) (bool, error) {
	var n int
	err := d.QueryRow(
		`SELECT COUNT(*) FROM trades WHERE mint=$1 AND side='buy'`, mint,
	).Scan(&n)
	return n > 0, err
}

// RecordSimulatedSellPnl grava o resultado de um sell simulado.
func (d *DB) RecordSimulatedSellPnl(
	tradeID, mint string,
	executedValueSOL, feesSOL, realizedPnlSOL float64,
	reason string,
) error {
	now := time.Now().UTC()
	_, err := d.Exec(
		`UPDATE trades
		 SET executed_value_sol=$1,
		     fees_sol=$2,
		     realized_pnl_sol=$3,
		     exit_reason=$4,
		     state='simulated',
		     reconciliation_needed=FALSE,
		     updated_at=$5
		 WHERE id=$6`,
		executedValueSOL, feesSOL, realizedPnlSOL, reason, now, tradeID,
	)
	return err
}

// ---------------------------------------------------------------------------
// Token events / assessments
// ---------------------------------------------------------------------------

// RecordEvent agora guarda os metadados do token (name, symbol, image_uri), a liquidez e o MarketCap.
func (d *DB) RecordEvent(mint, creator, raw string, at time.Time, latency int64, name, symbol, imageURI string, vSol, vTokens, marketCapSol float64) error {
	_, err := d.Exec(
		`INSERT INTO token_events(mint, creator, received_at, raw_json, latency_ms, name, symbol, image_uri, vsol_bonding_curve, vtoken_bonding_curve, market_cap_sol)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		 ON CONFLICT (mint, received_at) DO NOTHING`,
		mint, creator, at, raw, latency, name, symbol, imageURI, vSol, vTokens, marketCapSol,
	)
	return err
}

func (d *DB) RecordAssessment(mint string, approved bool, reason, profile string, score float64) error {
	_, err := d.Exec(
		`INSERT INTO risk_assessments(mint, approved, reason, entry_profile, entry_score, created_at)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		mint, approved, reason, profile, score, time.Now().UTC(),
	)
	return err
}

func (d *DB) TokenEventCount() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM token_events`).Scan(&n)
	return n, err
}

func (d *DB) RiskAssessmentCount() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM risk_assessments`).Scan(&n)
	return n, err
}

func (d *DB) ListTokens() (*sql.Rows, error) {
	return d.Query(
		`SELECT e.mint,
		        COALESCE(e.creator,''),
		        e.received_at,
		        e.latency_ms,
		        r.approved,
		        COALESCE(r.reason,''),
		        COALESCE(r.entry_profile,''),
		        COALESCE(r.entry_score,0),
		        COALESCE(t.side,''),
		        COALESCE(t.state,''),
		        COALESCE(p.status,''),
		        COALESCE(e.name,''),
		        COALESCE(e.symbol,''),
		        COALESCE(e.image_uri,''),
		        COALESCE(e.vsol_bonding_curve,0),
		        COALESCE(e.vtoken_bonding_curve,0),
		        COALESCE(e.market_cap_sol,0)
		 FROM token_events e
		 LEFT JOIN risk_assessments r
		        ON r.id = (SELECT MAX(r2.id) FROM risk_assessments r2 WHERE r2.mint = e.mint)
		 LEFT JOIN trades t
		        ON t.id = (SELECT t2.id FROM trades t2 WHERE t2.mint = e.mint ORDER BY t2.created_at DESC LIMIT 1)
		 LEFT JOIN positions p
		        ON p.mint = e.mint
		 ORDER BY e.id DESC
		 LIMIT 200`,
	)
}

// ---------------------------------------------------------------------------
// Positions
// ---------------------------------------------------------------------------

func (d *DB) HasOpenPosition(mint string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM positions WHERE mint=$1 AND status='open'`, mint).Scan(&n)
	return n > 0, err
}

func (d *DB) OpenPositionCount() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM positions WHERE status='open'`).Scan(&n)
	return n, err
}

func (d *DB) OpenPositionMints() ([]string, error) {
	rows, err := d.Query(`SELECT mint FROM positions WHERE status='open'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mints []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		mints = append(mints, m)
	}
	return mints, rows.Err()
}

func (d *DB) OpenPositionDetails(mint string) (cost float64, openedAt time.Time, ok bool, err error) {
	err = d.QueryRow(
		`SELECT COALESCE(cost_sol,0), opened_at
		 FROM positions WHERE mint=$1 AND status='open'`, mint,
	).Scan(&cost, &openedAt)
	if err == sql.ErrNoRows {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return cost, openedAt, true, nil
}

func (d *DB) InsertPosition(mint, quantity string, cost float64) error {
	now := time.Now().UTC()
	_, err := d.Exec(
		`INSERT INTO positions(mint, quantity, cost_sol, status, opened_at, updated_at)
		 VALUES($1,$2,$3,'open',$4,$5)
		 ON CONFLICT(mint) DO UPDATE
		 SET quantity = EXCLUDED.quantity,
		     cost_sol = EXCLUDED.cost_sol,
		     status   = 'open',
		     updated_at = EXCLUDED.updated_at`,
		mint, quantity, cost, now, now,
	)
	return err
}

func (d *DB) InsertPositionWithFees(
	mint, creator, quantity string,
	cost, fees float64,
) error {
	now := time.Now().UTC()
	_, err := d.Exec(
		`INSERT INTO positions(mint, creator, quantity, cost_sol, fees_sol, status, opened_at, updated_at)
		 VALUES($1,$2,$3,$4,$5,'open',$6,$7)
		 ON CONFLICT(mint) DO UPDATE
		   SET creator    = EXCLUDED.creator,
		       quantity   = EXCLUDED.quantity,
		       cost_sol   = EXCLUDED.cost_sol,
		       fees_sol   = EXCLUDED.fees_sol,
		       status     = 'open',
		       updated_at = EXCLUDED.updated_at`,
		mint, creator, quantity, cost, fees, now, now,
	)
	return err
}

func (d *DB) ClosePosition(mint string) error {
	_, err := d.Exec(
		`UPDATE positions
		 SET status='closed', updated_at=$1
		 WHERE mint=$2 AND status='open'`,
		time.Now().UTC(), mint,
	)
	return err
}

func (d *DB) PositionCost(mint string) (float64, error) {
	var cost float64
	err := d.QueryRow(
		`SELECT COALESCE(cost_sol,0) FROM positions WHERE mint=$1 AND status='open'`,
		mint,
	).Scan(&cost)
	return cost, err
}

func (d *DB) PositionFeesSOL(mint string) (float64, error) {
	var fees float64
	err := d.QueryRow(
		`SELECT COALESCE(fees_sol,0) FROM positions WHERE mint=$1 AND status='open'`,
		mint,
	).Scan(&fees)
	return fees, err
}

func (d *DB) PositionCreator(mint string) (string, error) {
	var creator sql.NullString
	err := d.QueryRow(
		`SELECT creator FROM positions WHERE mint=$1 AND status='open'`,
		mint,
	).Scan(&creator)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return creator.String, err
}

// ---------------------------------------------------------------------------
// Marks — preços em market cap SOL por modo
// ---------------------------------------------------------------------------

func (d *DB) markTable(simulation bool) string {
	if simulation {
		return "paper_marks"
	}
	return "live_marks"
}

func (d *DB) UpsertMark(simulation bool, mint string, mcapSOL float64, isEntry bool) error {
	tbl := d.markTable(simulation)
	now := time.Now().UTC()

	if isEntry {
		_, err := d.Exec(
			`INSERT INTO `+tbl+`(mint, entry_market_cap_sol, current_market_cap_sol, peak_market_cap_sol, price_source, updated_at)
			 VALUES($1,$2,$2,$2,'ws',$3)
			 ON CONFLICT(mint) DO UPDATE
			   SET current_market_cap_sol = EXCLUDED.current_market_cap_sol,
			       peak_market_cap_sol    = GREATEST(`+tbl+`.peak_market_cap_sol, EXCLUDED.current_market_cap_sol),
			       price_source           = 'ws',
			       updated_at             = EXCLUDED.updated_at`,
			mint, mcapSOL, now,
		)
		return err
	}

	_, err := d.Exec(
		`INSERT INTO `+tbl+`(mint, entry_market_cap_sol, current_market_cap_sol, peak_market_cap_sol, price_source, updated_at)
		 VALUES($1,$2,$2,$2,'ws',$3)
		 ON CONFLICT(mint) DO UPDATE
		   SET current_market_cap_sol = EXCLUDED.current_market_cap_sol,
		       peak_market_cap_sol    = GREATEST(`+tbl+`.peak_market_cap_sol, EXCLUDED.current_market_cap_sol),
		       price_source           = 'ws',
		       updated_at             = EXCLUDED.updated_at`,
		mint, mcapSOL, now,
	)
	return err
}

func (d *DB) Mark(simulation bool, mint string) (entry, current, peak float64, ok bool, err error) {
	tbl := d.markTable(simulation)
	row := d.QueryRow(
		`SELECT entry_market_cap_sol, current_market_cap_sol, peak_market_cap_sol
		 FROM `+tbl+` WHERE mint=$1`, mint,
	)
	err = row.Scan(&entry, &current, &peak)
	if err == sql.ErrNoRows {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, err
	}
	return entry, current, peak, true, nil
}

func (d *DB) DeleteMark(simulation bool, mint string) error {
	_, err := d.Exec(`DELETE FROM `+d.markTable(simulation)+` WHERE mint=$1`, mint)
	return err
}

// ---------------------------------------------------------------------------
// Simulação
// ---------------------------------------------------------------------------

func (d *DB) EnsureSimulationSession(balance float64) error {
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM simulation_sessions WHERE state='running'`).Scan(&n)
	if n > 0 {
		return nil
	}
	_, err := d.Exec(
		`INSERT INTO simulation_sessions(started_at, balance_start_sol, state)
		 VALUES($1,$2,'running')`,
		time.Now().UTC(), balance,
	)
	return err
}

func (d *DB) SimulationRunStart() (time.Time, error) {
	var startedAt time.Time
	err := d.QueryRow(
		`SELECT started_at FROM simulation_sessions
		 WHERE state='running'
		 ORDER BY id DESC LIMIT 1`,
	).Scan(&startedAt)
	return startedAt, err
}

func (d *DB) SimulationAvailableBalanceSOL(initialBalance float64) (float64, error) {
	var balance float64
	err := d.QueryRow(
		`SELECT $1
		        + COALESCE((SELECT SUM(t.realized_pnl_sol) FROM trades t
		                    WHERE t.state='simulated'
		                      AND t.side='sell'
		                      AND t.realized_pnl_sol IS NOT NULL),0)
		        - COALESCE((SELECT SUM(fees_sol) FROM trades WHERE state='simulated'),0)
		        - COALESCE((SELECT SUM(cost_sol) FROM positions WHERE status='open'),0)`,
		initialBalance,
	).Scan(&balance)
	return balance, err
}

// ---------------------------------------------------------------------------
// Logs / blacklist
// ---------------------------------------------------------------------------

func (d *DB) LogOperational(level, eventType, message, payload string) error {
	_, err := d.Exec(
		`INSERT INTO operational_logs(level, event_type, message, payload_json, created_at)
		 VALUES($1,$2,$3,$4,$5)`,
		level, eventType, message, payload, time.Now().UTC(),
	)
	return err
}

func (d *DB) IsCreatorBlacklisted(creator string) (bool, error) {
	if strings.TrimSpace(creator) == "" {
		return false, nil
	}
	var n int
	err := d.QueryRow(
		`SELECT COUNT(*) FROM creator_blacklist
		 WHERE creator=$1 AND blocked_at > $2`,
		creator, time.Now().UTC().Add(-24*time.Hour),
	).Scan(&n)
	return n > 0, err
}

func (d *DB) BlacklistCreator(creator, reason string) error {
	if strings.TrimSpace(creator) == "" {
		return nil
	}
	_, err := d.Exec(
		`INSERT INTO creator_blacklist(creator, blocked_at, reason)
		 VALUES($1, NOW(), $2)
		 ON CONFLICT(creator) DO UPDATE
		   SET blocked_at = EXCLUDED.blocked_at,
		       reason     = EXCLUDED.reason`,
		creator, reason,
	)
	return err
}

// ---------------------------------------------------------------------------
// Bot state (Controller)
// ---------------------------------------------------------------------------

func (d *DB) SetBotState(state string) error {
	_, err := d.Exec(
		`UPDATE bot_state SET state=$1, updated_at=NOW() WHERE id=1`, state,
	)
	return err
}

func (d *DB) GetBotState() (string, error) {
	var s string
	err := d.QueryRow(`SELECT state FROM bot_state WHERE id=1`).Scan(&s)
	return s, err
}
