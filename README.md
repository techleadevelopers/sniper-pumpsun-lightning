# Sniper Lightning

Bot automatizado para Pump.fun usando exclusivamente a PumpPortal Lightning API.

## Requisitos
- Go 1.22+
- PostgreSQL 15+
- API Key da PumpPortal (com carteira vinculada e saldo)

## Arranque rápido

1. Copia `.env.example` para `.env` e preenche as variáveis.
2. Cria as bases de dados no PostgreSQL:
   ```sql
   CREATE DATABASE sniper_paper;
   CREATE DATABASE sniper_live;