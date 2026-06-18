# Fees API

A billing and fee accrual service built with [Encore](https://encore.dev) and [Temporal](https://temporal.io). Models monthly billing periods as durable workflows, using Temporal signals for progressive fee accrual and automatic period-end closure.

## Overview

Each bill is represented as a Temporal workflow that:
1. Starts when a bill is created
2. Accepts line items via signals throughout the billing period
3. Auto-closes at period end (or can be manually closed via signal)
4. Persists all state to PostgreSQL via activities

Temporal is treated as the **first-class store of state** — all mutations flow through the workflow as signals, ensuring sequential processing and crash recovery.

## Architecture

```
HTTP Request → Encore API → Temporal Signal → Workflow → Activity → PostgreSQL
                                                ↑
                                          Durable Timer
                                       (auto-close at period end)
```

| Layer | File | Responsibility |
|-------|------|----------------|
| API | `fees/api.go` | HTTP endpoints, validation, signal dispatch |
| Workflow | `fees/workflow.go` | Signal-driven billing lifecycle |
| Activities | `fees/activities.go` | Side-effect execution (DB writes) |
| Repository | `fees/repository.go` | SQL operations with transactional safety |
| Models | `fees/models.go` | Request/response types, domain models |
| Client | `fees/temporal_client.go` | Temporal client helpers (start, signal, query) |
| Worker | `fees/worker.go` | Workflow/activity registration and polling |
| Service | `fees/service.go` | Encore service init, starts worker |
| Database | `fees/db.go` | Database resource declaration |

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Encore CLI](https://encore.dev/docs/install)
- [Temporal CLI](https://docs.temporal.io/cli)
- [Docker](https://www.docker.com/products/docker-desktop/) (for PostgreSQL)

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

## API Endpoints

### Create a Bill

```bash
curl -X POST http://localhost:4000/bills \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": "cust_123",
    "currency": "USD",
    "period_start": "2026-06-01T00:00:00Z",
    "period_end": "2026-06-30T23:59:59Z"
  }'
```

### Add a Line Item

Sends a signal to the bill's workflow. The item is persisted asynchronously.

```bash
curl -X POST http://localhost:4000/bills/1/line-items \
  -H "Content-Type: application/json" \
  -d '{
    "description": "API usage - June",
    "amount_minor": 4999,
    "currency": "USD"
  }'
```

### Close a Bill

Sends a close signal to the workflow. The bill is closed with the total computed from all line items.

```bash
curl -X POST http://localhost:4000/bills/1/close
```

### Get a Bill

Returns the bill and all line items from the database.

```bash
curl http://localhost:4000/bills/1
```

### Get Workflow State

Queries the Temporal workflow directly for real-time state (before DB persistence).

```bash
curl http://localhost:4000/bills/1/workflow-state
```

### List Bills

```bash
# All bills
curl http://localhost:4000/bills

# Filter by status
curl "http://localhost:4000/bills?status=OPEN"
curl "http://localhost:4000/bills?status=CLOSED"
```

## Design Decisions

### Money as `int64` (Minor Units)

All monetary values are stored in **minor units** (cents for USD, tetri for GEL):
- `$29.99` → `2999`
- Avoids floating-point precision errors (`0.1 + 0.2 ≠ 0.3`)
- Safe integer arithmetic for addition and summation

### Temporal Signals for Mutations

All bill modifications (add line item, close) go through Temporal signals rather than direct DB writes:
- **Sequential processing** — no race conditions between concurrent requests
- **Durability** — signals are persisted; if the worker crashes, work resumes
- **Audit trail** — Temporal's event history records every signal

### Dual State Store

- **Temporal** = authoritative state for active bills (real-time via query)
- **PostgreSQL** = queryable store for listing, filtering, and reporting

Activities persist to the database, keeping both in sync. The DB is eventually consistent with the workflow (milliseconds delay).

### `FOR UPDATE` Row Locking

Activities use `SELECT ... FOR UPDATE` within transactions to prevent concurrent modification at the database level — defense in depth alongside Temporal's sequential signal processing.

### Idempotent Close

Closing an already-closed bill returns the existing closed state rather than erroring. This supports Temporal's at-least-once activity delivery (retries on failure).

## Supported Currencies

- `USD` — United States Dollar (minor unit: cent)
- `GEL` — Georgian Lari (minor unit: tetri)

## Running Tests

```bash
encore test ./...
```

## Project Structure

```
fees-api/
├── encore.app                          # Encore app manifest
├── go.mod                              # Go module dependencies
├── README.md                           # This file
├── ARCHITECTURE.md                     # Detailed architecture documentation
└── fees/                               # Fees service
    ├── api.go                          # HTTP API endpoints
    ├── workflow.go                     # Temporal workflow (signal-driven)
    ├── activities.go                   # Temporal activities
    ├── temporal_client.go              # Start/signal/query helpers
    ├── worker.go                       # Worker registration
    ├── service.go                      # Encore service init
    ├── repository.go                   # Database operations
    ├── models.go                       # Types and constants
    ├── db.go                           # Database declaration
    └── migrations/
        └── 001_create_billing_tables.up.sql
```
