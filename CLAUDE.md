# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build and run (generates all outputs)
go run main.go

# Build binary
go build -o sector-radar main.go

# Run compiled binary
./sector-radar
```

```bash
# Run tests
go test ./...

# Lint
go vet ./...
```

## Architecture

Single-file Go application (`main.go`). On each run it:

1. **Ingests CSV data** — reads `omxspi*.csv`, `sx20pi*.csv`, `sx30pi*.csv`, `sx35pi*.csv`, `sx50pi*.csv` from the project root (falls back to `history/` subdirectory). Files are downloaded manually from Nasdaq Nordic. CSV format: semicolon-separated, columns `Date;Open;High;Close`.

2. **Fetches Riksbank yields** — calls `https://api.riksbank.se/swea/v1/Observations/{SEGVB2YC|SEGVB10YC}/{from}/{to}` for Swedish 2Y and 10Y government bond yields.

3. **Merges incrementally into `market_data.json`** — acts as a persistent cache; existing entries are preserved and new dates are upserted.

4. **Generates `rotation_dashboard.html`** — uses `go-echarts` to render three charts (RS lines, yield curves, yield spread), then post-processes the HTML via `injectDashboardEnhancements()` to inject a sticky nav bar, regime analysis panels (1Y/1M/1W), a macro playbook, and a JS timeframe switcher.

5. **Exports `llm_signals.json`** — machine-readable sector rankings, momentum, crossover signals, and regime summary for the 1M window.

## Key domain concepts

**Four sectors tracked** (relative to OMXSPI benchmark):
- SX50 — Industrials (export-driven, cyclical)
- SX35 — Real Estate (rate-sensitive)
- SX30 — Banks/Financials (yield-curve play)
- SX20 — Health Care (defensive)

**RS (Relative Strength)** = `(SectorIndex / OMXSPI) * 100`. Rising RS = outperforming, capital inflow. For 1M/1W views the dashboard rebases all RS values to 100 at the window start via JS.

**Regime analysis** runs at three lookback windows — `1Y` (252 days), `1M` (22 days), `1W` (5 days) — with window-specific thresholds for yield movement and spread change.

## Deployment

GitHub Actions (`.github/workflows/deploy.yml`) runs `go run main.go` daily at UTC 16:30, copies outputs to `public/`, and deploys to Cloudflare Pages. Requires secrets: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`.
