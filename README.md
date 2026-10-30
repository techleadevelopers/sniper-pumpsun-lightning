# Sniper Lightning

Bot automatizado de trading na Pump.fun com execução delegada à **PumpPortal Lightning API** e dados de mercado via **PumpPortal WebSocket**. Sem RPC próprio, sem Jito, sem Helius, sem assinatura local de transações.

---

### 1. Objetivo

Reproduzir operacionalmente o ciclo manual de sniping:

1. Detetar tokens novos via WebSocket.
2. Avaliar oportunidades com filtros determinísticos (perfil + momentum).
3. Comprar via Lightning API quando os critérios são satisfeitos.
4. Acompanhar a posição com marks de market cap vindas do WebSocket.
5. Vender automaticamente quando uma regra de saída é atingida.
6. Registar PnL, fees e motivo de fecho.

A inteligência (o quê, quando, porquê) é **nossa**. A execução (assinar, encaminhar, submeter à Solana) é **da PumpPortal**.

---

### 2. Arquitetura

```
PumpPortal WebSocket
├── subscribeNewToken (global, gratuito)
└── subscribeTokenTrade (por mint aberta, pago a partir de 2026)
│
│ frames JSON
▼
internal/tokenmonitor
├── handleFrame: distingue "sinal de entrada" vs "atualização de preço"
├── Momentum tracker (janelas short / medium / long)
├── Profile selector (first_pump / momentum)
└── TrackMint / UntrackMint (subscrições dinâmicas)
│
├──────────────────────────────┐
│ buyFn(mint, creator, mcap) │ UpsertMark(current, peak)
▼ ▼
internal/execution internal/database
├── BuyWithMark ├── paper_marks / live_marks
├── Sell (paper, com PnL) ├── positions
└── Sell (live) ├── trades
│ └── creator_blacklist
│ HTTP POST
▼
PumpPortal Lightning
POST /api/trade?api-key=...
│
│ signature
▼
internal/positions
├── Manager.Run (loop a cada PRICE_POLL_SECONDS)
├── ExitReason (TP / SL / trailing / max hold)
└── PaperPnLSOL
│
│ Sell(ctx, mint, slippage, fee, reason)
▼
(volta ao engine)
```


**Princípios**

- **Fonte única de execução** — todas as ordens passam por `internal/execution/pumpportal.Client`.
- **Fonte única de dados de mercado** — todos os preços vêm do WebSocket da PumpPortal. Nenhum RPC externo é consultado.
- **Decisão separada da execução** — `positions.ExitReason` devolve uma string; `engine.Sell` executa. Nunca o contrário.
- **Preço verificado ou nada** — sem mark no banco, o Manager não decide. Nunca inventa um preço médio.

---

### 3. Estrutura de Ficheiros
```
sniper-lightning/
├── cmd/
│ └── server/
│ └── main.go # entrypoint (arranque, wiring, shutdown)
├── internal/
│ ├── api/
│ │ └── api.go # HTTP + WebSocket para o dashboard
│ ├── config/
│ │ └── config.go # carregamento do .env
│ ├── database/
│ │ ├── database.go # acesso a PostgreSQL
│ │ └── migrations/postgres/
│ │ ├── 001_init.sql # schema base
│ │ └── 002_realistic_marks.sql # marks, fees, bot_state
│ ├── events/
│ │ └── bus.go # pub/sub in-process
│ ├── execution/
│ │ ├── engine.go # BuyWithMark, Sell, limites
│ │ └── pumpportal/
│ │ ├── client.go # cliente HTTP da Lightning
│ │ ├── request.go # tipos de payload
│ │ └── errors.go # erros tipados
│ ├── positions/
│ │ ├── exit.go # ExitReason (TP/SL/trailing/max hold)
│ │ ├── pnl.go # PaperPnLSOL
│ │ └── manager.go # loop de avaliação de saídas
│ └── tokenmonitor/
│ ├── monitor.go # WS, handleFrame, TrackMint
│ ├── momentum.go # MomentumTracker
│ ├── profiles.go # SelectProfile
│ ├── marketdata.go # cache in-memory (legado)
│ └── holders.go # interface para contagem de holders
├── Dockerfile
├── .dockerignore
├── .env.example
├── go.mod
├── go.sum
└── README.md
```


---

### 4. Requisitos

| Componente | Versão mínima | Notas |
|---|---|---|
| Go | 1.22 | Necessário para `embed` e `slog` |
| PostgreSQL | 15 | 16 recomendado |
| Conta PumpPortal | — | API Key com carteira vinculada e saldo (só em modo live) |

**Não é necessário:** Helius, QuickNode, Jito, RPC próprio, serviço de assinatura Solana.

---

### 5. Variáveis de Ambiente

Copia `.env.example` para `.env` **localmente** e nunca faças commit do `.env`. No Railway, define-as no dashboard (Variables).

**5.1 Modo e execução**

| Variável | Default | Descrição |
|---|---|---|
| `SIMULATION` | `true` | `true` = modo paper. `false` = modo real (exige `ENABLE_LIVE_TRADING=true`) |
| `ENABLE_LIVE_TRADING` | `false` | Guard de segurança. `SIMULATION=false` sem isto aborta o arranque |
| `HTTP_ADDR` | `:8080` | Endereço da API HTTP |
| `ADMIN_PASSWORD` | — | Password do dashboard (HMAC para tokens) |
| `CORS_ALLOWED_ORIGINS` | — | Origens permitidas, separadas por vírgula |

**5.2 PumpPortal**

| Variável | Descrição |
|---|---|
| `PUMPPORTAL_API_KEY` | Chave da Lightning API (carteira vinculada) |
| `PUMPPORTAL_URL` | Endpoint Lightning — default `https://pumpportal.fun/api/trade` |
| `PUMPPORTAL_WS` | WebSocket — default `wss://pumpportal.fun/api/data` |
| `WALLET_PUBLIC_ADDRESS` | Endereço público da carteira (só informativo) |

**5.3 Base de dados**

| Variável | Descrição |
|---|---|
| `SIMULATION_DATABASE_URL` | Postgres para paper. Ex.: `postgres://user:pass@host:5432/sniper_paper` |
| `REAL_DATABASE_URL` | Postgres para live. Ex.: `postgres://user:pass@host:5432/sniper_live` |

Em Railway: cria `SIMULATION_DATABASE_URL` e `REAL_DATABASE_URL` como *reference* à `DATABASE_URL` do serviço Postgres.

**5.4 Trading**

| Variável | Descrição |
|---|---|
| `TRADE_USD` | **Atenção:** o nome está errado, mas o valor é interpretado como SOL. Usa 0.005–0.02 |
| `MAX_TRADE_SOL` | Limite duro por trade |
| `MAX_EXPOSURE_SOL` | Exposição máxima agregada |
| `MAX_DAILY_LOSS_SOL` | Circuit breaker diário |
| `FEE_RESERVE_SOL` | Margem de segurança para fees |
| `MAX_OPEN_POSITIONS` | Máximo de posições abertas |
| `MAX_SLIPPAGE_BPS` | Slippage máximo em basis points (1500 = 15%) |
| `MAX_PRIORITY_FEE_SOL` | Priority fee por transação |

**5.5 Saídas**

| Variável | Descrição |
|---|---|
| `TAKE_PROFIT_GROSS_PCT` | Take profit em percentagem |
| `STOP_LOSS_PCT` | Stop loss em percentagem |
| `TRAILING_STOP_PCT` | Trailing stop a partir do pico |
| `TRAILING_STOP_ARM_PCT` | Lucro mínimo para armar o trailing |
| `MAX_HOLD_SECONDS` | Tempo máximo em posição |

**5.6 Scanner e momentum**

| Variável | Descrição |
|---|---|
| `ENTRY_DELAY_SECONDS` | Delay entre deteção e compra |
| `DEDUPE_TTL_MS` | Janela de deduplicação de tokens |
| `TOKEN_MAX_AGE_SECONDS` | Idade máxima do token à entrada |
| `PRICE_POLL_SECONDS` | Intervalo do Manager (verificação de saídas) |
| `MOMENTUM_*` | Janelas e limiares do tracker de momentum |

**5.7 Perfis**

| Variável | Descrição |
|---|---|
| `ENTRY_PROFILE_MODE` | `auto` \| `first_pump_only` \| `momentum_only` |
| `FIRST_PUMP_*` | Filtros do perfil first_pump |
| `MOMENTUM_PROFILE_*` | Filtros do perfil momentum |

---

### 6. Arranque Local

```bash
# 1. Clonar
git clone <repo> sniper-lightning && cd sniper-lightning

# 2. Configurar
cp .env.example .env
# editar .env (API key, password, DATABASE_URL)

# 3. Criar bases de dados (PostgreSQL local)
createdb sniper_paper
createdb sniper_live

# 4. Compilar e correr
go mod tidy
go build ./...
go run ./cmd/server

---

### 7. Saida no Arranque:
```
INFO  modo de arranque  {"simulation": true, "live_trading_enabled": false}
INFO  PumpPortal WS conectado; subscrito a subscribeNewToken
INFO  position manager iniciado  {"simulation": true, "poll_interval": "20s"}
INFO  HTTP server a ouvir  {"addr": ":8080"}
```

### 8. Verificação:

``
curl http://localhost:8080/api/health
# {"status":"ok","eventMonitorStatus":"connected","checkedAt":"..."}
```

### 9. Ciclo de Vida de uma Operação:

>9.1 Entrada:

```
WS frame (subscribeNewToken)
  → monitor.handleFrame
    → filtros globais (mcap, idade, duplicação)
    → MomentumTracker.Evaluate
    → SelectProfile → Approved?
      → DB.RecordAssessment
      → DB.RecordEvent
      → delay (ENTRY_DELAY_SECONDS)
      → buyFn(mint, creator, mcap, profile, score)
        → engine.BuyWithMark
          → DB.HasPurchaseForMint (uma compra por mint, para sempre)
          → DB.IsCreatorBlacklisted
          → reserva in-memory (evita duplicado concorrente)
          → DB.InsertTrade (prepared)
          → [paper]  DB.InsertPositionWithFees
                     DB.UpsertMark(entry, isEntry=true)
                     DB.UpdateTradeState(simulated)
          → [live]   portal.Buy → signature
                     DB.InsertPositionWithFees
                     DB.UpsertMark(entry)
                     DB.UpdateTradeState(submitted)

```


>9.2 Atualizaçãodo Preço:

```
Manager.tick (a cada PRICE_POLL_SECONDS)
  → DB.OpenPositionMints
  → para cada mint:
      → monitor.TrackMint (subscreve subscribeTokenTrade)
      → WS envia trades desta mint
      → monitor.onTrackedTrade
        → DB.UpsertMark(current, isEntry=false)
          (atualiza current + peak via GREATEST, não toca em entry)
```

>9.3 Saída:
```
Manager.tick
  → DB.Mark(entry, current, peak)
  → DB.OpenPositionDetails(cost, openedAt)
  → ExitReason(Position, currentValue, liquid=true, limits, now)
    → "" (nada)
    → "stop loss"        (única razão que bloqueia creator)
    → "trailing stop"
    → "take profit"
    → "max hold (gain|loss|flat)"
  → se ≠ "":
    → engine.Sell(ctx, mint, slippage, fee, reason)
      → [paper]  DB.Mark → PaperPnLSOL
                 DB.ClosePosition
                 DB.RecordSimulatedSellPnl
                 DB.DeleteMark
                 [se stop loss] DB.BlacklistCreator
      → [live]   portal.Sell → signature
                 DB.ClosePosition
                 DB.DeleteMark
                 DB.UpdateTradeState(submitted)
    → monitor.UntrackMint
```

# 9. Schema da Base de Dados

Aplicado automaticamente via `db.Migrate()` no arranque. As migrações são embebidas (`//go:embed`).

| Tabela | Função |
|---|---|
| `schema_migrations` | Controlo de versões aplicadas |
| `token_events` | Histórico bruto de tokens detetados |
| `risk_assessments` | Decisões de entrada (approved, reason, score) |
| `trades` | Ordens enviadas (buy/sell), estado e PnL |
| `positions` | Posições abertas/fechadas com custo e fees |
| `paper_marks` | Preços em market cap (modo paper) |
| `live_marks` | Preços em market cap (modo live) |
| `simulation_sessions` | Saldo inicial de cada sessão paper |
| `creator_blacklist` | Criadores bloqueados por stop loss |
| `operational_logs` | Logs de eventos operacionais |
| `bot_state` | Estado global do controller (running/paused/stopped) |
| `settings` | Configuração dinâmica (chave-valor) |

**Índices críticos**

- `idx_trades_one_active_per_mint` — partial unique index que impede 2 trades ativos para a mesma mint.
- `idx_trades_simulated_sell_confirmed` — acelera o cálculo de saldo disponível.
- `idx_token_events_received_at` — acelera listagens do dashboard.


# 10. Segurança

## 10.1 Cadeia de confiança

- **Chave Lightning**: dá controlo total sobre os fundos da carteira vinculada. Trata-a como uma private key.
- Nunca coloques a chave no frontend, no repo ou em logs.
- Nunca comites o `.env` (está no `.gitignore` e no `.dockerignore`).
- Nunca abras a porta `5432` à internet. O bot liga-se por localhost ou pela rede interna do Railway.

## 10.2 Guards de arranque

```go
if !cfg.Simulation && !cfg.EnableLiveTrading {
    log.Fatal("SIMULATION=false mas ENABLE_LIVE_TRADING=false — abortar")
}
```

Para entrar em modo real é necessário **dois flags explícitos**. Um `.env` mal copiado não entra em live por acidente.

## 10.3 Limites duros no engine

Antes de qualquer compra, o `engine.BuyWithMark` valida:

- `MaxTradeSOL` — valor por trade
- `MaxExposureSOL` — exposição total
- `MaxDailyLossSOL` — perdas diárias
- `MaxOpenPositions` — posições abertas
- `FeeReserveSOL` — margem de segurança
- `HasPurchaseForMint` — uma compra por mint, para sempre
- `IsCreatorBlacklisted` — 24h de bloqueio após stop loss

Nenhum destes limites pode ser contornado pelo monitor ou pelo Manager — vivem no engine, o único ponto de execução.

# 11. Limitações Inerentes à Arquitetura Lightning-only

**Esta secção é obrigatória antes de considerar modo real.**

## 11.1 Sem reconciliação on-chain

A Lightning API devolve uma **assinatura**, não um recibo de execução. O estado do trade fica em `submitted` e não é verificado on-chain por este bot.

**Consequência:** um trade pode ter falhado on-chain (slippage, conta fechada, instrução revertida) e o bot não sabe. A posição fica aberta no banco mesmo que os tokens nunca tenham chegado à carteira.

**Mitigação recomendada antes de live:**

- Verificar manualmente no Solscan as primeiras N transações.
- Adicionar futuramente um RPC read-only (não para assinar, só para `getSignatureStatuses` e `getTransaction`) para confirmar o estado dos trades.

## 11.2 Preço derivado de market cap, não de reservas

As marks vêm do frame `marketCapSol` da PumpPortal. Isso é o preço que o emissor do WebSocket calcula — não uma leitura direta das reservas on-chain.

**Consequência:** em períodos de volatilidade extrema ou lag do WS, o `current_market_cap_sol` pode estar desatualizado por segundos. O `ExitReason` decide com base nesse valor.

**Mitigação:** `ExitTriggerBufferPct=0.5` no `main.go` já absorve oscilações pequenas. Não uses `TAKE_PROFIT_GROSS_PCT` abaixo de 10% em live.

## 11.3 Sem PnL real em live (por agora)

O `engine.Sell` em modo live regista o trade mas não preenche `realized_pnl_sol` (só o modo paper o faz, via `PaperPnLSOL`). Para PnL real, precisas dos saldos SOL antes/depois da carteira — o que exige RPC read-only.

**Mitigação:** o dashboard mostra `executed_value_sol` e `fees_sol` — podes calcular PnL manualmente por período.

## 11.4 Custo das subscrições WS

Segundo a documentação da PumpPortal, a partir de maio/2026 `subscribeTokenTrade` custa **0.01 SOL por cada 10k trades transmitidos**. `subscribeNewToken` e `subscribeMigration` continuam gratuitas.

**Mitigação:** o monitor subscreve por mint aberta (`TrackMint`) e dessubscreve quando fecha (`UntrackMint`). Se correres com muitas posições simultâneas, o custo cresce. `MAX_OPEN_POSITIONS` limita isto indiretamente.



# 12. Observabilidade

## 12.1 Logs (zap)

Eventos estruturados chave-valor. Eventos principais:

| Evento | Significado |
|---|---|
| `buy_confirmed` / `buy_sent` | Ordem de compra enviada |
| `buy_failed` | Compra falhou |
| `sell_confirmed` / `sell_sent` | Ordem de venda enviada |
| `sell_failed` | Venda falhou |
| `token_rejected` | Perfil rejeitou o token |
| `risk_alert` | Algo requer atenção humana |
| `price_update` | Mark atualizada (útil para o dashboard) |
| `exit trigger` | Manager vai disparar venda (com reason) |

## 12.2 Endpoints HTTP

| Rota | Autenticação | Descrição |
|---|---|---|
| `GET /api/health` | Não | Estado do WS + timestamp |
| `POST /api/auth/login` | Password | Devolve token HMAC (24h) |
| `GET /api/dashboard` | Bearer / Cookie | Contadores agregados |
| `GET /api/tokens` | Bearer / Cookie | Últimos 200 eventos + assessments |
| `GET /api/trades` | Bearer / Cookie | Últimos 200 trades |
| `WS /ws/events` | Não | Stream de eventos do bus |

## 12.3 Event bus

Todos os eventos (`buy_*`, `sell_*`, `price_update`, `exit trigger`, `risk_alert`) passam pelo `events.Bus`. O dashboard liga-se por WebSocket e recebe-os em tempo real.


# 13. Diagnóstico de Problemas

## 13.1 `"eventMonitorStatus": "disconnected"`

- Verifica `PUMPPORTAL_WS`. Deve ser `wss://pumpportal.fun/api/data`.
- Verifica saída de rede — Railway/EC2 têm de permitir **WSS outbound**.
- O `monitor.Run` reconecta com backoff exponencial (1s → 30s).

## 13.2 `"no verified mark for mint"` no `engine.Sell`

- O Manager chamou `engine.Sell` mas `paper_marks`/`live_marks` está vazia.
- Causa típica: monitor não subscreveu `subscribeTokenTrade` para a mint.
- Verifica que `TrackMint` foi chamado: log `WS: subscrito a trades`.

## 13.3 `"mint already has a purchase"`

- `HasPurchaseForMint` bloqueia reentradas.
- Se quiseres reentrar numa mint, tens de apagar o trade histórico manualmente (não é recomendado).

## 13.4 Migração falha no arranque

- `002_realistic_marks.sql` requer que `positions` exista (é aplicada depois de `001`).
- Se a migração `002` falhar a meio, apaga a linha correspondente de `schema_migrations` e reinicia.

## 13.5 `"insufficient wallet balance"` em live

- O `engine.reserve` verifica exposição e saldo antes de comprar.
- Verifica `WALLET_PUBLIC_ADDRESS` e garante que a carteira tem o saldo reservado para fees.



# 14. Estado Atual do Projeto

| Componente | Estado |
|---|---|
| Ligação WS + reconexão | ✅ Implementado |
| Deteção de tokens novos | ✅ Implementado |
| Perfis `first_pump` + `momentum` | ✅ Implementado |
| `BuyWithMark` (paper + live) | ✅ Implementado |
| `Sell` com PnL paper | ✅ Implementado |
| Position Manager (loop de saída) | ✅ Implementado |
| Tracking dinâmico de trades por mint | ✅ Implementado |
| Creator blacklist após stop loss | ✅ Implementado |
| Guards de arranque (`ENABLE_LIVE_TRADING`) | ✅ Implementado |
| Reconciliação on-chain | ❌ Não implementada (ver §11.1) |
| PnL real em live | ❌ Não implementado (ver §11.3) |
| Circuit breaker por RPC | ❌ Não aplicável (sem RPC) |
| Notificações (Telegram/Discord) | ❌ Não implementado |

# 15. Roadmap Antes de Modo Real

Requisitos mínimos para ativar `SIMULATION=false` com capital real:

1. **7 dias consecutivos em paper** com PnL consistente no dashboard.
2. **Verificação manual de 20 transações** live-preview no Solscan (com `ENABLE_LIVE_TRADING=false` e `SIMULATION=false` — o bot prepara mas não assina).
3. **Adicionar RPC read-only** para `getSignatureStatuses` e `getTransaction` (opcional mas recomendado) — não substitui a Lightning, só confirma o resultado.
4. **PnL real** calculado a partir de `preBalance`/`postBalance` da carteira.
5. **Testar circuit breakers**: forçar `MAX_DAILY_LOSS_SOL` a disparar e verificar que o bot para.
6. **Capital mínimo**: começar com `MAX_TRADE_SOL=0.005` e `MAX_EXPOSURE_SOL=0.05` até 30 trades sem falha.

