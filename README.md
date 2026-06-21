# Fees API

A billing and fee accrual service built with [Encore](https://encore.dev) and [Temporal](https://temporal.io). Models monthly billing periods as durable workflows, with cross-currency support, API key authentication, rate limiting, and dynamic currency management.

## Overview

Each bill is represented as a Temporal workflow that:
1. Starts when a bill is created (scoped to the authenticated customer)
2. Accepts line items via signals — including cross-currency items with automatic FX conversion
3. Auto-closes at period end (or can be manually closed via signal)
4. Persists all state to PostgreSQL via activities

Temporal is treated as the **first-class store of state** — all mutations flow through the workflow as signals, ensuring sequential processing and crash recovery.

## Architecture

```
HTTP Request → Auth (API Key) → Rate Limit → Encore API → Temporal Signal → Workflow → Activity → PostgreSQL
                                                                              ↑
                                                                        Durable Timer
                                                                     (auto-close at period end)
```

| Layer | File | Responsibility |
|-------|------|----------------|
| API | `fees/api.go` | HTTP endpoints, validation, signal dispatch |
| Auth | `fees/auth.go` | API key authentication, brute-force protection |
| Rate Limit | `fees/ratelimit.go` | Per-customer rate limiting, brute-force protection |
| Audit | `fees/audit.go` | Event-based audit trail |
| Workflow | `fees/workflow.go` | Signal-driven billing lifecycle |
| FX Workflow | `fees/fx_workflow.go` | Temporal cron for daily FX rate fetching |
| Activities | `fees/activities.go` | Side-effect execution (DB writes) |
| FX Activities | `fees/fx_activities.go` | FX rate fetch/store activities |
| Repository | `fees/repository.go` | SQL operations, FX conversion logic |
| Models | `fees/models.go` | Request/response types, domain models |
| Alpha Vantage | `fees/alphavantage.go` | FX rate provider client |
| Customers | `fees/customer_api.go`, `fees/customer_repository.go` | Customer CRUD, API key management |
| Currencies | `fees/currency_api.go`, `fees/currency_repository.go` | Dynamic currency registration |
| FX Seed | `fees/fx_seed.go` | Historical FX rate seeding utility |
| Client | `fees/temporal_client.go` | Temporal client helpers (start, signal, query) |
| Worker | `fees/worker.go` | Workflow/activity registration and polling |
| Service | `fees/service.go` | Encore service init, starts worker |
| Database | `fees/db.go` | Database resource declaration |

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Encore CLI](https://encore.dev/docs/install)
- [Temporal CLI](https://docs.temporal.io/cli)
- [Docker](https://www.docker.com/products/docker-desktop/) (for PostgreSQL)
- [Alpha Vantage API Key](https://www.alphavantage.co/support/#api-key) (for FX rates)

## Installation

```bash
# Install Encore
brew install encoredev/tap/encore

# Install Temporal CLI
brew install temporal

# Clone the repository
git clone <repo-url>
cd fees-api
```

### Secrets Setup

```bash
# Set the Alpha Vantage API key
encore secret set --type local AlphaVantageAPIKey
```

Or create a `.secrets.local.cue` file:
```cue
AlphaVantageAPIKey: "your-key-here"
```

## Running Locally

**Terminal 1** — Start Temporal dev server:
```bash
temporal server start-dev --namespace default
```

**Terminal 2** — Start the Encore app:
```bash
encore run
```

The app starts at `http://localhost:4000` with the Temporal worker running in the background.

### Dashboards

- Encore Dashboard: http://localhost:9400 (API docs, traces, request flow)
- Temporal Web UI: http://localhost:8233 (workflow executions, event history)

## Authentication

All billing endpoints require API key authentication via Bearer token:

```bash
curl -H "Authorization: Bearer fee_abc123..." http://localhost:4000/bills
```

### Create a Customer (public endpoint)

```bash
curl -X POST http://localhost:4000/customers \
  -H "Content-Type: application/json" \
  -d '{"name": "Acme Corp", "email": "billing@acme.com"}'
```

Response includes the API key (shown only once):
```json
{
  "customer": {"id": "cust_...", "name": "Acme Corp", "email": "billing@acme.com"},
  "api_key": "fee_..."
}
```

### Rate Limiting

- **Per-customer**: 60 requests per minute
- **Brute-force protection**: 10 failed auth attempts per key hash per minute

## API Endpoints

### Bills (authenticated)

#### Create a Bill

```bash
curl -X POST http://localhost:4000/bills \
  -H "Authorization: Bearer fee_..." \
  -H "Content-Type: application/json" \
  -d '{
    "currency": "USD",
    "period_start": "2026-06-01T00:00:00Z",
    "period_end": "2026-06-30T23:59:59Z"
  }'
```

#### Add a Line Item (supports cross-currency)

```bash
curl -X POST http://localhost:4000/bills/1/line-items \
  -H "Authorization: Bearer fee_..." \
  -H "Content-Type: application/json" \
  -d '{
    "description": "API usage - June",
    "amount_minor": 4999,
    "currency": "GEL",
    "date": "2026-06-15"
  }'
```

If the line item currency differs from the bill currency, the system automatically converts the amount using the FX rate for the specified date (with previous-day fallback). Conversion uses USD triangulation.

#### Cancel a Line Item

Removes a line item from an open bill and decrements the running total.

```bash
curl -X DELETE http://localhost:4000/bills/1/line-items/1 \
  -H "Authorization: Bearer fee_..."
```

#### Close a Bill

```bash
curl -X POST http://localhost:4000/bills/1/close \
  -H "Authorization: Bearer fee_..."
```

#### Get a Bill

```bash
curl http://localhost:4000/bills/1 \
  -H "Authorization: Bearer fee_..."
```

#### List Bills

```bash
curl "http://localhost:4000/bills?status=OPEN" \
  -H "Authorization: Bearer fee_..."
```

### Currencies

#### List Currencies (public)

```bash
curl http://localhost:4000/currencies
```

#### Add Currency (private — admin only)

```bash
curl -X POST http://localhost:4000/currencies \
  -H "Content-Type: application/json" \
  -d '{"code": "EUR", "name": "Euro", "decimal_places": 2}'
```

The `decimal_places` field indicates how many digits represent the minor unit (e.g., 2 for USD/EUR/GEL, 0 for JPY, 3 for BHD). Valid range: 0–4.

Adding a currency verifies Alpha Vantage data availability and atomically seeds 30 days of historical rates.

### FX Seed (private — admin only)

```bash
curl -X POST http://localhost:4000/fx/seed \
  -H "Content-Type: application/json" \
  -d '{"days": 30}'
```

## Cross-Currency Support

### FX Rate Storage

All FX rates are stored as `1 USD = X` (quote currency). Conversion between any two currencies uses **USD triangulation**:

- `USD → GEL`: multiply by rate
- `GEL → USD`: divide by rate
- `EUR → GEL`: divide by EUR rate, multiply by GEL rate

### Daily FX Cron

A Temporal cron workflow runs daily at 9am UTC, fetching current rates from Alpha Vantage for all active non-USD currencies. If all currencies fail to update, the workflow returns an error (visible in Temporal UI).

### Rate Lookup

When adding a cross-currency line item, the system looks up the FX rate for the specified date, falling back to the previous day if no rate exists for that exact date.

## Design Decisions

### Money as `int64` (Minor Units)

All monetary values are stored in **minor units** — the smallest denomination of each currency:
- USD (2 decimal places): `$29.99` → `2999`
- GEL (2 decimal places): `₾5.50` → `550`
- JPY (0 decimal places): `¥1000` → `1000`

Each currency's `decimal_places` field tells consumers how to convert between minor units and display values (divide by `10^decimal_places`). This avoids floating-point precision errors (`0.1 + 0.2 ≠ 0.3`) and ensures safe integer arithmetic for all summation.

### Temporal Signals for Mutations

All bill modifications (add line item, close) go through Temporal signals rather than direct DB writes:
- **Sequential processing** — no race conditions between concurrent requests
- **Durability** — signals are persisted; if the worker crashes, work resumes
- **Audit trail** — Temporal's event history records every signal

### Dual State Store

- **Temporal** = authoritative state for active bills (real-time via query)
- **PostgreSQL** = queryable store for listing, filtering, and reporting

Activities persist to the database, keeping both in sync. The DB is eventually consistent with the workflow (milliseconds delay).

### Customer Isolation

Each customer can only access their own bills. Ownership is enforced at the API layer via the authenticated customer ID from the auth handler.

### Atomic Currency Registration

Adding a new currency performs all external API calls (Alpha Vantage verification + historical rate fetch) before starting a database transaction. The DB transaction atomically inserts the currency and seeds all rates — if anything fails, nothing is committed.

## Running Tests

```bash
# Run all tests
encore test ./fees/ -v

# Run only workflow unit tests
encore test ./fees/ -run TestWorkflow -v

# Run only repository/integration tests
encore test ./fees/ -run "^Test[^W]" -v
```

**Note:** Tests must be run via `encore test`, not `go test`, because the Encore runtime manages database provisioning and migrations.

## Project Structure

```
fees-api/
├── encore.app                          # Encore app manifest
├── go.mod                              # Go module dependencies
├── README.md                           # This file
└── fees/                               # Fees service
    ├── api.go                          # HTTP API endpoints (auth-protected)
    ├── auth.go                         # API key auth handler
    ├── audit.go                        # Audit event logging
    ├── ratelimit.go                    # Rate limiting + brute-force protection
    ├── workflow.go                     # Temporal workflow (signal-driven)
    ├── fx_workflow.go                  # FX rate cron workflow
    ├── activities.go                   # Billing activities
    ├── fx_activities.go                # FX rate activities
    ├── alphavantage.go                 # Alpha Vantage API client
    ├── temporal_client.go              # Start/signal/query helpers
    ├── worker.go                       # Worker registration
    ├── service.go                      # Encore service init
    ├── repository.go                   # Billing DB operations + FX conversion
    ├── customer_api.go                 # Customer endpoints
    ├── customer_repository.go          # Customer DB operations
    ├── currency_api.go                 # Currency management endpoints
    ├── currency_repository.go          # Currency DB operations
    ├── fx_seed.go                      # Historical FX rate seeding
    ├── models.go                       # Types and constants
    ├── db.go                           # Database declaration
    └── migrations/
        ├── 001_create_billing_tables.up.sql
        ├── 002_create_fx_rates.up.sql
        ├── 003_create_customers.up.sql
        ├── 004_create_currencies.up.sql
        ├── 005_add_bills_customer_status_index.up.sql
        └── 006_add_decimal_places.up.sql
```
