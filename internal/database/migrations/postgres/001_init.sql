CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS token_events (
    id BIGSERIAL PRIMARY KEY,
    mint TEXT NOT NULL,
    creator TEXT,
    received_at TIMESTAMPTZ NOT NULL,
    raw_json TEXT,
    latency_ms BIGINT,
    UNIQUE(mint, received_at)
);

CREATE TABLE IF NOT EXISTS risk_assessments (
    id BIGSERIAL PRIMARY KEY,
    mint TEXT NOT NULL,
    approved BOOLEAN NOT NULL,
    reason TEXT NOT NULL,
    entry_profile TEXT,
    entry_score DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS trades (
    id TEXT PRIMARY KEY,
    mint TEXT NOT NULL,
    side TEXT NOT NULL,
    quantity TEXT NOT NULL,
    requested_value_sol DOUBLE PRECISION NOT NULL,
    executed_value_sol DOUBLE PRECISION,
    fees_sol DOUBLE PRECISION,
    slippage_pct DOUBLE PRECISION NOT NULL,
    signature TEXT,
    state TEXT NOT NULL,
    entry_reason TEXT,
    exit_reason TEXT,
    entry_profile TEXT,
    entry_score DOUBLE PRECISION,
    realized_pnl_sol DOUBLE PRECISION,
    reconciliation_needed BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS positions (
    mint TEXT PRIMARY KEY,
    quantity TEXT NOT NULL,
    cost_sol DOUBLE PRECISION NOT NULL,
    status TEXT NOT NULL,
    opened_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS simulation_sessions (
    id BIGSERIAL PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    balance_start_sol DOUBLE PRECISION NOT NULL,
    state TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS creator_blacklist (
    creator TEXT PRIMARY KEY,
    blocked_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS operational_logs (
    id BIGSERIAL PRIMARY KEY,
    level TEXT NOT NULL,
    event_type TEXT NOT NULL,
    message TEXT NOT NULL,
    payload_json TEXT,
    created_at TIMESTAMPTZ NOT NULL
);