-- ============================================================
-- 002_realistic_marks.sql
-- Acrescenta suporte a simulação realista (paper/live marks),
-- fees por posição, bot_state e índices que faltavam.
-- ============================================================

-- ---------- settings (chave/valor) ----------
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------- bot_state (única linha) ----------
CREATE TABLE IF NOT EXISTS bot_state (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    state TEXT NOT NULL DEFAULT 'stopped',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO bot_state(id, state) VALUES (1, 'stopped')
    ON CONFLICT (id) DO NOTHING;

-- ---------- simulation_sessions: garante coluna state ----------
ALTER TABLE simulation_sessions
    ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'running';
ALTER TABLE simulation_sessions
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ---------- positions: fees por posição ----------
ALTER TABLE positions
    ADD COLUMN IF NOT EXISTS fees_sol DOUBLE PRECISION NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_positions_status ON positions(status);

-- ---------- trades: índices úteis ----------
CREATE INDEX IF NOT EXISTS idx_trades_created_at ON trades(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_mint       ON trades(mint);

-- Um único trade ativo por mint (evita duplicados enquanto a 1ª ainda está em curso)
CREATE UNIQUE INDEX IF NOT EXISTS idx_trades_one_active_per_mint
    ON trades(mint)
    WHERE state IN ('prepared','submitted','pending','unknown');

-- ---------- token_events / risk_assessments: índices ----------
CREATE INDEX IF NOT EXISTS idx_token_events_mint
    ON token_events(mint);
CREATE INDEX IF NOT EXISTS idx_token_events_received_at
    ON token_events(received_at DESC);

CREATE INDEX IF NOT EXISTS idx_risk_assessments_mint
    ON risk_assessments(mint);

-- ---------- paper_marks (só simulação) ----------
CREATE TABLE IF NOT EXISTS paper_marks (
    mint                  TEXT PRIMARY KEY,
    entry_market_cap_sol  DOUBLE PRECISION NOT NULL,
    current_market_cap_sol DOUBLE PRECISION NOT NULL,
    peak_market_cap_sol   DOUBLE PRECISION NOT NULL DEFAULT 0,
    price_source          TEXT NOT NULL DEFAULT 'unverified',
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------- live_marks (só modo real) ----------
CREATE TABLE IF NOT EXISTS live_marks (
    mint                  TEXT PRIMARY KEY,
    entry_market_cap_sol  DOUBLE PRECISION NOT NULL,
    current_market_cap_sol DOUBLE PRECISION NOT NULL,
    peak_market_cap_sol   DOUBLE PRECISION NOT NULL DEFAULT 0,
    price_source          TEXT NOT NULL DEFAULT 'unverified',
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------- creator_blacklist: reason opcional ----------
ALTER TABLE creator_blacklist
    ADD COLUMN IF NOT EXISTS reason TEXT;