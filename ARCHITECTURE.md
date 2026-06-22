# Fees API — Architecture & Technology Guide

This document explains Temporal and Encore.dev, and walks through how they work together in this project step by step.

---

## Table of Contents

1. [What is Encore.dev?](#what-is-encoredev)
2. [What is Temporal?](#what-is-temporal)
3. [How They Fit Together](#how-they-fit-together)
4. [Project Structure](#project-structure)
5. [Step-by-Step Flow](#step-by-step-flow)
6. [Temporal Signals Explained](#temporal-signals-explained)
7. [Temporal Queries Explained](#temporal-queries-explained)
8. [Key Design Decisions](#key-design-decisions)

---

## What is Encore.dev?

**Encore** is a Go backend framework that removes infrastructure boilerplate. Instead of manually configuring HTTP servers, databases, message queues, etc., you declare what you need via code annotations and resource declarations, and Encore handles the rest.

### Key features used in this project:

| Feature | How it works |
|---------|-------------|
| **API endpoints** | Add `//encore:api` comment above a function → Encore generates HTTP handler, request parsing, response serialization |
| **SQL databases** | Call `sqldb.NewDatabase("name", config)` → Encore provisions PostgreSQL, manages connections, runs migrations |
| **Structured errors** | Use `errs.Error{Code, Message}` → Encore maps to proper HTTP status codes |
| **Local dev** | Run `encore run` → full backend with hot-reload, tracing dashboard at localhost:9400, auto-managed DB |

### Example from this codebase:

```go
// fees/db.go — One line declares "I need a PostgreSQL database"
var db = sqldb.NewDatabase("fees", sqldb.DatabaseConfig{Migrations: "./migrations"})

// fees/api.go — One annotation creates a POST endpoint
//encore:api public method=POST path=/bills
func CreateBill(ctx context.Context, req *CreateBillRequest) (*CreateBillResponse, error) {
    // ...
}
```

No router setup. No `http.ListenAndServe()`. No connection string management. Encore handles all of it.

---

## What is Temporal?

**Temporal** is a workflow orchestration engine. Think of it as a "durable function runner" — you write normal-looking sequential code, but Temporal guarantees it will complete even if your server crashes, restarts, or the logic takes days/months.

### Core Concepts:

| Concept | What it is | Analogy |
|---------|-----------|---------|
| **Workflow** | A long-running, durable function. Its execution state is persisted by Temporal. If the server dies, the workflow resumes exactly where it left off. | A state machine that survives crashes |
| **Activity** | A unit of side-effect work (DB write, API call). Can be retried automatically on failure. | A function call that Temporal can replay |
| **Worker** | A process that polls Temporal for tasks and executes workflows/activities. | A background job processor |
| **Signal** | An external message sent INTO a running workflow to inject data or trigger behavior. | Pushing a message into a mailbox |
| **Query** | A read-only request to peek at a workflow's current state without affecting it. | A GET request to the workflow |
| **Task Queue** | A named queue connecting workflows to workers. | A job queue name |

### Why Temporal instead of a simple background job?

1. **Durability**: A `workflow.Sleep(30 * 24 * time.Hour)` survives server restarts. A `time.Sleep` doesn't.
2. **Exactly-once semantics**: Activities are retried on failure but not double-executed on success.
3. **State as code**: The workflow IS the state machine — no separate DB table tracking "billing_state".
4. **Signals**: External systems can push data into a running workflow (e.g., "add this line item").
5. **Visibility**: You can query any workflow's current state at any time.

### Temporalite

Temporalite is a lightweight, single-binary version of Temporal for local development. Instead of running Temporal Server + PostgreSQL + Elasticsearch in Docker, you just run one binary. Same API, just simpler setup.

---

## How They Fit Together

```
┌─────────────────────────────────────────────────────────────────┐
│                        Encore Application                        │
│                                                                   │
│  ┌─────────────┐     ┌──────────────────┐     ┌──────────────┐  │
│  │  API Layer  │────▶│  Temporal Client  │────▶│  Temporalite │  │
│  │  (api.go)   │     │(temporal_client.go│     │  (external)  │  │
│  └─────┬───────┘     └──────────────────┘     └──────┬───────┘  │
│        │                                              │          │
│        │                                              ▼          │
│        │                                      ┌──────────────┐   │
│        │                                      │    Worker     │   │
│        │                                      │  (worker.go)  │   │
│        │                                      └──────┬───────┘   │
│        │                                              │          │
│        │              ┌──────────────────┐            │          │
│        │              │    Workflow       │◀───────────┘          │
│        │              │  (workflow.go)    │                       │
│        │              └──────┬───────────┘                       │
│        │                     │                                   │
│        │                     ▼                                   │
│        │              ┌──────────────────┐                       │
│        │              │   Activities     │                       │
│        │              │ (activities.go)  │                       │
│        │              └──────┬───────────┘                       │
│        │                     │                                   │
│        ▼                     ▼                                   │
│  ┌─────────────────────────────────────────┐                    │
│  │         PostgreSQL (Encore-managed)       │                    │
│  │         repository.go / db.go             │                    │
│  └─────────────────────────────────────────┘                    │
└─────────────────────────────────────────────────────────────────┘
```

- **Encore** handles: HTTP API, database, request/response serialization, local dev environment
- **Temporal** handles: Mutation sequencing, signal processing, automatic retries, durable timer-based auto-close
- **PostgreSQL** is the system of record (all reads, all persistent state), while **Temporal** owns the ordering and delivery guarantees for mutations

---

## Project Structure

```
fees-api/
├── encore.app                 # Encore app manifest
├── go.mod                     # Go module (imports encore.dev + temporal SDK)
├── fees/                      # "fees" service (package name = service name in Encore)
│   ├── db.go                  # Database resource declaration
│   ├── models.go              # Request/response types, domain models
│   ├── api.go                 # HTTP API endpoints (auth-protected)
│   ├── auth.go                # API key auth handler + brute-force protection
│   ├── audit.go               # Audit event logging
│   ├── ratelimit.go           # Per-customer rate limiting
│   ├── workflow.go            # Temporal workflow definition (signal-driven)
│   ├── fx_workflow.go         # FX rate cron workflow (daily at 9am UTC)
│   ├── activities.go          # Billing activities (DB side-effects)
│   ├── fx_activities.go       # FX rate fetch/store activities
│   ├── alphavantage.go        # Alpha Vantage API client
│   ├── temporal_client.go     # Temporal client helpers (start, signal, query)
│   ├── worker.go              # Temporal worker setup
│   ├── repository.go          # Billing DB operations + FX conversion (USD triangulation)
│   ├── customer_api.go        # Customer CRUD endpoints
│   ├── customer_repository.go # Customer DB operations + API key management
│   ├── currency_api.go        # Dynamic currency registration
│   ├── currency_repository.go # Currency DB operations
│   ├── fx_seed.go             # Historical FX rate seeding utility
│   ├── service.go             # Encore service init, starts workers
│   └── migrations/
│       ├── 001_create_billing_tables.up.sql
│       ├── 002_create_fx_rates.up.sql
│       ├── 003_create_customers.up.sql
│       ├── 004_create_currencies.up.sql
│       ├── 005_add_bills_customer_status_index.up.sql
│       └── 006_add_decimal_places.up.sql
```

---

## Step-by-Step Flow

### 1. Creating a Bill

```
Client → POST /bills → CreateBill (api.go)
                           │
                           ├── 1. Insert bill row into PostgreSQL (repository.go)
                           │      Status: OPEN, TotalAmount: 0
                           │
                           ├── 2. Start Temporal workflow (temporal_client.go)
                           │      Workflow ID: "fee-period-bill-{id}"
                           │      Input: bill ID, customer, currency, period dates
                           │      ⚠️ If this fails → DELETE the bill row (rollback)
                           │
                           └── 3. Store workflow ID in bill row
                                  Return bill to client
```

**What happens inside Temporal:**
- A new workflow execution is created and persisted
- The workflow initializes its in-memory state
- Registers a query handler (so we can read state)
- Opens signal channels (add_line_item, close_bill)
- Starts a timer goroutine for auto-close at period end
- Enters the main loop: `for state.Status == BillStatusOpen { selector.Select(ctx) }`
- The workflow is now **waiting** — consuming zero resources until a signal arrives

### 2. Adding a Line Item (via Signal)

```
Client → POST /bills/:id/line-items → AddLineItem (api.go)
                                          │
                                          ├── 1. Validate input (currency exists in DB, amount, etc.)
                                          ├── 2. Check bill exists & is open (fast-fail from DB)
                                          └── 3. Send "add_line_item" SIGNAL to workflow
                                                    │        (includes optional idempotency_key)
                                                    ▼
                                          ┌─────────────────────┐
                                          │  Workflow receives   │
                                          │  signal, wakes up    │
                                          │                       │
                                          │  Check idempotency:  │
                                          │  Skip if key seen    │
                                          │                       │
                                          │  Executes Activity:  │
                                          │  AddLineItemActivity │
                                          │     │                 │
                                          │     ▼                 │
                                          │  INSERT into DB       │
                                          │                       │
                                          │  Updates in-memory    │
                                          │  state (total, items) │
                                          │  Records key as seen  │
                                          │                       │
                                          │  Goes back to sleep   │
                                          └─────────────────────┘
```

**Key insight**: The API doesn't write to the DB directly for mutations. It sends a signal. The workflow receives it, persists via an activity (writing to PostgreSQL as the system of record), and updates its in-memory projection. Temporal owns the sequencing; Postgres owns the data.

### 3. Closing a Bill (via Signal)

```
Client → POST /bills/:id/close → CloseBill (api.go)
                                      │
                                      └── Send "close_bill" SIGNAL to workflow
                                                │
                                                ▼
                                      ┌─────────────────────┐
                                      │  Workflow receives   │
                                      │  signal, wakes up    │
                                      │                       │
                                      │  Executes Activity:  │
                                      │  CloseBillActivity   │
                                      │     │                 │
                                      │     ▼                 │
                                      │  UPDATE bill status   │
                                      │  = CLOSED, compute    │
                                      │  total_amount_minor   │
                                      │                       │
                                      │  state.Status=CLOSED  │
                                      │  Loop exits           │
                                      │  Workflow COMPLETES   │
                                      └─────────────────────┘
```

### 4. Auto-Close at Period End

If no one sends a close signal, the timer goroutine fires when `time.Now() >= PeriodEnd`:

```
Temporal timer fires → timerFired channel receives
                          │
                          ▼
                    Same as manual close, but reason = "period_ended"
```

This is a **durable timer** — it survives server restarts. If period_end is 30 days away, Temporal remembers.

### 5. Querying Workflow State

```
Client → GET /bills/:id/workflow-state → GetBillWorkflowState (api.go)
                                              │
                                              └── Query Temporal workflow (QueryBillState)
                                                       │
                                                       ▼
                                              Workflow's query handler returns
                                              current BillWorkflowState (real-time)
```

### 6. Querying from Database

```
Client → GET /bills/:id → GetBill (api.go)
                              │
                              └── SELECT from PostgreSQL (eventually consistent with workflow)
```

---

## Temporal Signals Explained

### What is a Signal?

A signal is an **asynchronous message sent to a running workflow**. The workflow defines named channels and listens for messages on them.

### In this codebase:

**Defining signal channels** (workflow.go):
```go
addLineItemCh := workflow.GetSignalChannel(ctx, "add_line_item")
closeBillCh := workflow.GetSignalChannel(ctx, "close_bill")
```

**Listening for signals** (workflow.go):
```go
selector := workflow.NewSelector(ctx)
selector.AddReceive(addLineItemCh, func(c workflow.ReceiveChannel, more bool) {
    var signal AddLineItemSignal
    c.Receive(ctx, &signal)
    // Process the signal...
})
selector.Select(ctx) // Blocks until a signal arrives
```

**Sending signals** (temporal_client.go):
```go
err = client.SignalWorkflow(ctx, workflowID, "", "add_line_item", AddLineItemSignal{
    Description: "Monthly subscription",
    AmountMinor: 2999,
    Currency:    "USD",
})
```

### Signal flow:

1. External code calls `client.SignalWorkflow(...)` with a workflow ID + signal name + payload
2. Temporal server durably stores the signal
3. The workflow's signal channel receives it
4. The selector wakes up, invokes the handler
5. The handler runs an activity (DB write) and updates workflow state
6. The selector loops back, waiting for the next signal

### Why signals instead of direct DB writes?

- **Ordering**: Signals are processed sequentially — no race conditions
- **Consistency**: Workflow state and DB stay in sync (activity succeeds → state updates)
- **Durability**: If the worker crashes mid-signal, Temporal replays from the last checkpoint
- **Auditability**: Temporal's event history records every signal received

---

## Temporal Queries Explained

### What is a Query?

A query is a **synchronous, read-only** call to peek at a workflow's current in-memory state. It doesn't change anything — just reads.

### In this codebase:

**Registering a query handler** (workflow.go):
```go
workflow.SetQueryHandler(ctx, "bill_state", func() (BillWorkflowState, error) {
    return state, nil
})
```

**Calling a query** (temporal_client.go):
```go
resp, err := client.QueryWorkflow(ctx, workflowID, "", "bill_state")
var state BillWorkflowState
resp.Get(&state)
```

This lets you get the **real-time** bill state (items, total, status) directly from the workflow's memory — even before activities have committed to the DB.

---

## Key Design Decisions

### 1. Money stored as `int64` (minor units)

All monetary values are stored as **integers representing the smallest denomination** of a currency — not as decimals or floats. This is the same approach used by Stripe, Adyen, and most payment processors.

#### What are minor units?

Every currency has a "minor unit" — the smallest coin or subdivision:

| Currency | Minor unit | Name | Example |
|----------|-----------|------|---------|
| USD | 1/100 dollar | cent | `$29.99` → `2999` |
| GEL | 1/100 lari | tetri | `₾5.50` → `550` |
| JPY | 1 yen (no subdivision) | — | `¥1000` → `1000` |

Instead of storing `29.99` as a float or decimal, we store `2999` as an integer. The API consumer and display layer are responsible for dividing by 100 (or the appropriate factor) when presenting to users.

#### Where minor units appear in the codebase

**Database schema** — all money columns are `BIGINT`:
```sql
-- bills table
total_amount_minor BIGINT NOT NULL DEFAULT 0

-- bill_line_items table
base_amount_minor BIGINT NOT NULL   -- amount in the line item's original currency
bill_amount_minor BIGINT NOT NULL   -- converted amount in the bill's currency
```

**Go structs** — all money fields are `int64`:
```go
type Bill struct {
    TotalAmountMinor int64  // running total in bill currency
}

type LineItem struct {
    BaseAmountMinor int64  // original amount (e.g., 5000 = 50.00 GEL)
    BillAmountMinor int64  // converted amount (e.g., 1818 = $18.18)
}
```

**API requests** — clients send integer amounts:
```json
{
  "description": "Monthly subscription",
  "amount_minor": 4999,
  "currency": "USD"
}
```

#### Why not `float64`?

IEEE 754 floating-point cannot represent all decimal fractions exactly:
```
0.1 + 0.2 == 0.30000000000000004  (not 0.3)
```

Over thousands of line items, these errors compound. A billing system that's off by even one cent per transaction is broken. Integers eliminate this entirely: `10 + 20 == 30`, always.

#### Why not `NUMERIC`/`DECIMAL` in the database?

PostgreSQL `NUMERIC` is exact, but:
- Slower for aggregation (SUM) and comparison than `BIGINT`
- Requires more storage (variable-length vs fixed 8 bytes)
- Still needs application-layer handling of scale/precision

`BIGINT` is simpler, faster, and equally exact for discrete minor units.

#### Why `int64` over `int`?

`int64` is explicitly 64-bit on all platforms (~9.2 quintillion maximum). Plain `int` is 32-bit on some architectures, which maxes out at ~$21 million in cents — too small for production billing.

#### The one place floats appear: FX rates

FX rates (`fx_rates.rate`) are stored as `NUMERIC(18,8)` in the DB and `float64` in Go. This is acceptable because:
- Rates are **multipliers**, not money — precision loss in the 8th decimal is negligible
- The conversion result is immediately rounded to the nearest minor unit via `math.Round`, making the final monetary value exact

#### Cross-currency conversion example

A line item of **50.00 GEL** on a **USD bill** with FX rate `1 USD = 2.75 GEL`:

```
Input:  base_amount_minor = 5000       (50.00 GEL in tetri)
Step 1: Convert to float for division:  float64(5000) / 2.75 = 1818.18...
Step 2: Round to nearest minor unit:    math.Round(1818.18) = 1818
Output: bill_amount_minor = 1818       ($18.18 in cents, stored as int64)
```

The code (`repository.go`):
```go
func convertAmountViaUSD(baseAmountMinor int64, baseCurrency, billCurrency Currency, rateBase, rateBill float64) int64 {
    if billCurrency == CurrencyUSD {
        return int64(math.Round(float64(baseAmountMinor) / rateBase))
    }
    // ... other cases
}
```

Both `5000` and `1818` are stored as exact integers. The float exists only for the brief multiplication/division step, then is immediately rounded back to a discrete integer.

#### Running total

The bill's `total_amount_minor` is updated atomically on each line item addition:
```sql
UPDATE bills SET total_amount_minor = total_amount_minor + $2 WHERE id = $1
```

Since both values are integers, this addition is always exact — no drift over time.

### 2. Idempotent line item signals

Clients can provide an `idempotency_key` when adding a line item. The workflow tracks processed keys in memory:

```go
// In the signal handler:
if signal.IdempotencyKey != "" {
    if _, seen := processedKeys[signal.IdempotencyKey]; seen {
        return // already processed
    }
}
```

This prevents duplicate charges when clients retry after network timeouts. The key is optional — if omitted, every signal is treated as unique.

### 3. Transactional bill creation with rollback

If the Temporal workflow fails to start after the bill row is created, the bill is immediately deleted:

```go
workflowID, err := startFeeWorkflow(ctx, bill)
if err != nil {
    // Roll back: delete the orphaned bill
    deleteBill(ctx, bill.ID)
    return nil, mapError(err)
}
```

This prevents "zombie" bills sitting OPEN forever with no workflow to process signals or auto-close.

### 4. State architecture: PostgreSQL as system of record

- **PostgreSQL** = system of record for all billing data (bills, line items, totals, customers)
- **Temporal** = sequencing and delivery layer that ensures mutations are processed exactly once, in order, with automatic retries

The workflow persists to DB via activities. The workflow also maintains an in-memory projection (queryable via `GET /bills/:id/workflow-state`) for real-time visibility into the processing pipeline. All read endpoints serve from Postgres.

### 5. Async acknowledgment for mutations

`AddLineItem` and `CloseBill` return immediately with `{"accepted": true}`. The actual work happens asynchronously in the workflow. This is because:
- The signal is durably stored by Temporal — it will be processed
- The workflow might need milliseconds to run the activity
- The client doesn't need to wait for the DB write

### 6. Validation before signalling

The API validates input and checks bill status before sending signals. This gives fast feedback for obvious errors (wrong currency, closed bill) without waiting for the workflow.

### 7. Period-end auto-close via durable timer

```go
workflow.Sleep(ctx, periodEndDuration) // survives restarts!
```

No cron job needed. No external scheduler. Temporal's durable timer handles month-long waits natively.

---

## Running Locally

1. Start Temporal: `temporal server start-dev --namespace default`
2. Start the Encore app: `encore run`
3. The worker starts automatically via `service.go` init
4. Access the API at `http://localhost:4000`
5. Access Encore dashboard at `http://localhost:9400`
6. Access Temporal UI at `http://localhost:8233`

The Temporal host defaults to `localhost:7233`. For production, set the `TemporalHostPort` secret (e.g., `encore secret set --type production TemporalHostPort`).

---

## API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/customers` | public | Create customer, returns API key |
| GET | `/customers/me` | auth | Get current customer info |
| POST | `/customers/rotate-key` | auth | Rotate API key |
| POST | `/bills` | auth | Create a new bill + start workflow |
| POST | `/bills/:id/line-items` | auth | Signal workflow to add a line item (cross-currency supported) |
| DELETE | `/bills/:id/line-items/:itemId` | auth | Signal workflow to cancel a line item |
| POST | `/bills/:id/close` | auth | Signal workflow to close the bill |
| GET | `/bills/:id` | auth | Get bill + line items from DB |
| GET | `/bills/:id/workflow-state` | auth | Query real-time state from Temporal |
| GET | `/bills` | auth | List customer's bills (optional `?status=OPEN\|CLOSED`) |
| GET | `/currencies` | public | List active currencies |
| POST | `/currencies` | private | Register new currency (internal mesh only, not internet-exposed) |
| POST | `/fx/seed` | private | Seed historical FX rates (internal mesh only) |
