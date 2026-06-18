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
- **Temporal** handles: Durable workflow state, signal processing, automatic retries, timer-based auto-close
- **Database** is the read-optimized store (for queries/listing), while **Temporal** is the authoritative state (for mutations)

---

## Project Structure

```
fees-api/
├── encore.app                 # Encore app manifest
├── go.mod                     # Go module (imports encore.dev + temporal SDK)
├── fees/                      # "fees" service (package name = service name in Encore)
│   ├── db.go                  # Database resource declaration
│   ├── models.go              # Request/response types, domain models
│   ├── api.go                 # HTTP API endpoints (Encore annotations)
│   ├── workflow.go            # Temporal workflow definition (signal-driven)
│   ├── activities.go          # Temporal activities (DB side-effects)
│   ├── temporal_client.go     # Temporal client helpers (start, signal, query)
│   ├── worker.go              # Temporal worker setup
│   ├── repository.go          # Database operations (CRUD)
│   └── migrations/
│       └── 001_create_billing_tables.up.sql
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
                                          ├── 1. Validate input (currency, amount, etc.)
                                          ├── 2. Check bill exists & is open (fast-fail from DB)
                                          └── 3. Send "add_line_item" SIGNAL to workflow
                                                    │
                                                    ▼
                                          ┌─────────────────────┐
                                          │  Workflow receives   │
                                          │  signal, wakes up    │
                                          │                       │
                                          │  Executes Activity:  │
                                          │  AddLineItemActivity │
                                          │     │                 │
                                          │     ▼                 │
                                          │  INSERT into DB       │
                                          │                       │
                                          │  Updates in-memory    │
                                          │  state (total, items) │
                                          │                       │
                                          │  Goes back to sleep   │
                                          └─────────────────────┘
```

**Key insight**: The API doesn't write to the DB directly. It sends a signal. The workflow receives it, persists via an activity, and updates its own state. This makes Temporal the "first-class store of state."

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

```go
TotalAmountMinor int64  // 2999 = $29.99
AmountMinor      int64  // cents for USD, tetri for GEL
```

**Why not float?** Floating-point arithmetic causes rounding errors (`0.1 + 0.2 != 0.3`). Financial systems must be exact.

**Why not string?** Can't do arithmetic without parsing.

**Why int64 over int?** int64 gives a guaranteed 64-bit range (~9.2 quintillion) regardless of platform.

### 2. Dual state: Temporal + PostgreSQL

- **Temporal** = authoritative state for active bills (real-time, signal-driven)
- **PostgreSQL** = queryable store for all bills (supports listing, filtering, reporting)

The workflow persists to DB via activities, so both stay in sync. The DB is eventually consistent with the workflow (milliseconds delay).

### 3. Async acknowledgment for mutations

`AddLineItem` and `CloseBill` return immediately with `{"accepted": true}`. The actual work happens asynchronously in the workflow. This is because:
- The signal is durably stored by Temporal — it will be processed
- The workflow might need milliseconds to run the activity
- The client doesn't need to wait for the DB write

### 4. Validation before signalling

The API validates input and checks bill status before sending signals. This gives fast feedback for obvious errors (wrong currency, closed bill) without waiting for the workflow.

### 5. Period-end auto-close via durable timer

```go
workflow.Sleep(ctx, periodEndDuration) // survives restarts!
```

No cron job needed. No external scheduler. Temporal's durable timer handles month-long waits natively.

---

## Running Locally

1. Start Temporalite: `temporalite start --namespace default`
2. Start the Encore app: `encore run`
3. Start the worker (in another terminal or via the app's init)
4. Access the API at `http://localhost:4000`
5. Access Encore dashboard at `http://localhost:9400`
6. Access Temporal UI at `http://localhost:8233`

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/bills` | Create a new bill + start workflow |
| POST | `/bills/:id/line-items` | Signal workflow to add a line item |
| POST | `/bills/:id/close` | Signal workflow to close the bill |
| GET | `/bills/:id` | Get bill + line items from DB |
| GET | `/bills/:id/workflow-state` | Query real-time state from Temporal |
| GET | `/bills` | List all bills (optional `?status=OPEN\|CLOSED` filter) |
