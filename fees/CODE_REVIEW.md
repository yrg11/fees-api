# Fees API — Senior/Staff Review

> Review of the Fees API submission against the Pave Coding Challenge 2026 (v0.8).
> Static review (Encore/Temporal/Docker were not booted to execute the test suite).

## Verdict

This is a **well-above-average submission**. It cleanly satisfies the literal feature
checklist, the money-type question is answered correctly and documented, the Temporal
workflow is deterministic and properly tested, and the author went well beyond scope
(cross-currency FX, auth, rate limiting, dynamic currencies). The code is readable and
the package layout is clean.

However, **the breadth came at the cost of depth on the one thing that matters most in a
billing system: the correctness of the money path.** The central architectural decision —
returning `{"accepted": true}` and processing every mutation asynchronously through a
fire-and-forget signal — actively undermines two of the six hard requirements and
introduces a double-billing class of bug. The challenge explicitly flags "What are the
correct semantics for the API?" and this is precisely where the design is weakest. A
staff engineer should have made the core accrual path bulletproof before adding Alpha
Vantage integration.

---

## Requirements coverage

| # | Requirement | Status | Notes |
|---|-------------|--------|-------|
| 1 | Create new bill | ✅ | `POST /bills` + starts workflow |
| 2 | Add line item to open bill | ⚠️ | Works, but async-ack hides failures (see A1) |
| 3 | Close bill, indicate **total + all line items** | ⚠️ | Close returns only `{accepted, bill_id}` — **not** the total or line items (see A2) |
| 4 | Reject line item if bill closed | ⚠️ | "Rejection" is best-effort at API and **silent** at workflow (see A1) |
| 5 | Query open & closed bills | ✅ | `GET /bills?status=`, `GET /bills/:id`, plus live workflow query |
| 6 | Handle GEL & USD | ✅✅ | Exceeds — full cross-currency with FX triangulation |
| — | Use Encore + temporalite | ✅ | Uses `temporal server start-dev` (the modern temporalite successor) |
| — | Use Temporal signals | ✅ | `add_line_item`, `close_bill` |
| — | README + unit tests | ✅ | Two good docs; solid workflow + repo tests |
| — | Single workflow per monthly bill | ✅ | `fee-period-bill-{id}`, durable auto-close timer |

---

## A. Architecture-level critique (the important stuff)

### A1. Async fire-and-forget signals defeat requirement #4 and break the API contract

The API validates against the DB, fires `SignalWorkflow`, and immediately returns
`accepted: true`. The actual work happens later in the workflow, where the result is
swallowed:

```go
// workflow.go
if err == nil {
    state.LineItems = append(...)
    state.TotalAmountMinor += output.LineItem.BillAmountMinor
}
// else: silently ignored — no retry-exhausted handling, no DLQ, no state change
```

Consequences:

- **Closed-bill rejection is a lie.** If a bill closes between the API's status check and
  the signal arriving, the workflow has already exited its loop; the signal is
  `drainSignals`'d and discarded — yet the client got `200 accepted`. Requirement #4 says
  *reject*; the system *accepts then silently drops*.
- **Invalid currency / missing FX rate are accepted then dropped.** `AddLineItem` only
  calls `validateCurrency` (format: `len >= 3`), not `validateCurrencyExists`. `"ZZZ"`
  passes, returns `accepted: true`, then the activity fails `ErrInvalidCurrency` 5×, then
  the error is swallowed. The customer is told their charge was accepted; it never lands.
  Same for `ErrFXRateNotFound`. All of `mapError`'s FX/currency branches are effectively
  **dead code on the add path** because nothing surfaces to the caller.
- **Permanent activity failure = silently lost revenue** with a success response to the
  client.

**Recommendation:** For request/response mutations, use **Temporal Update**
(`workflow.SetUpdateHandler` with a validator) instead of a signal. The validator rejects
a closed bill *synchronously* (clean fix for #4), and the update returns the persisted
line item. Keep a *signal* for the end-of-month "collect" event (which is genuinely
fire-and-forget, and satisfies "use temporal signals"). If you want to stay signal-only,
the API must at least wait for the workflow to acknowledge processing (query/poll or block
on the activity) before returning anything other than `202`.

### A2. Close doesn't fulfill 3a/3b — and the data to do so is computed then thrown away

Requirement 3 explicitly wants close to "indicate total amount being charged" and
"indicate all line items being charged." `CloseBill` returns `{accepted, bill_id}`.
Meanwhile `CloseBillActivity` *does* compute exactly the right thing:

```go
return CloseBillActivityOutput{ Bill: bill, LineItems: items }, nil
```

...and the workflow discards it: `ExecuteActivity(ctx, CloseBillActivity, ...).Get(ctx, nil)`.
So the literal requirement is unmet while the implementation already produces the answer.
A synchronous close (Update, or sync settle) should return total + line items.

### A3. "Temporal as a first-class store of state" is claimed but not actually achieved

The challenge sets this as a design directive, and the README asserts it. In reality
**PostgreSQL is the store of record** and Temporal is a sequencing/signal layer holding a
*redundant* in-memory copy:

- Bills are created by a **direct DB write in the API** before the workflow exists, so the
  README's "all mutations flow through the workflow as signals" is false for create.
- Every read endpoint (`GetBill`, `ListBills`) reads Postgres, not the workflow.
- The workflow's `state.TotalAmountMinor` can **diverge** from the DB (see B1) and is
  ultimately overwritten by the DB's `SUM` at close.

This is a legitimate architecture, but it's the *opposite* of the framing. Be honest in
the writeup: "Postgres is the system of record for queryability; Temporal owns sequencing
and the durable period timer." Don't claim Temporal is the source of truth when it isn't.
(A reviewer who wrote that sentence in the prompt will probe it.)

---

## B. Correctness bugs

### B1. At-least-once activities → duplicate line items → double billing (high severity)

`AddLineItemActivity` → `addLineItem` does an unconditional `INSERT ... RETURNING` with a
fresh `BIGSERIAL`. Temporal activities are **at-least-once**: if the DB commit succeeds but
the worker dies before reporting completion, Temporal retries → a second row → the customer
is charged twice. There is no idempotency key anywhere on the line-item path. For a billing
system this is the canonical Temporal foot-gun, and this challenge is *literally* about
billing under at-least-once delivery.

**Fix:** carry a client-supplied idempotency key (or a deterministic key derived in the
workflow, e.g. `workflow.GetInfo` + a sequence) into the signal/activity, add a `UNIQUE`
constraint, and `ON CONFLICT DO NOTHING`/return-existing. Same idea for `POST /bills` and
`POST /customers` (neither is idempotent today — a retried create makes a second bill +
second workflow).

### B2. Silent error swallowing in the workflow

Covered in A1 — `if err == nil` with no `else`. After retries are exhausted, the workflow
should fail the run, or move the item to a failure list reflected in state, or emit an
alert. Silently continuing in `OPEN` with a lost charge is unacceptable for money.

### B3. `getTemporalClient` caches the dial error forever (`sync.Once`)

```go
temporalClientOnce.Do(func() {
    temporalClient, temporalClientErr = client.Dial(...)
})
```

If Temporal isn't reachable on the first call (very common locally — Encore boots
before/with the temporal server), the error is cached permanently. Every subsequent request
returns the stale error and **the app can't recover without a full restart.** Combined with
`log.Fatalf` inside the worker goroutine in `initService`, a transient Temporal hiccup can
kill the process. Use a retrying connect, and only cache a *successful* client.

### B4. Orphaned bills on partial failure

`CreateBill` = (1) DB insert → (2) start workflow → (3) update workflow_id. If (2) fails,
the bill row sits `OPEN` forever with no workflow and no auto-close. If (3) fails, the
workflow runs but the DB lacks the ID. No compensation/saga. Consider creating the bill
inside the workflow (via an activity) and using `SignalWithStartWorkflow`, or compensating
the DB row on start failure.

### B5. `queryBillWorkflowState` fallback is cargo-cult / dead code

```go
if err := resp.Get(&state); err != nil {
    raw, _ := json.Marshal(resp)        // marshals the EncodedValue wrapper, not the state
    json.Unmarshal(raw, &state)         // cannot produce BillWorkflowState
}
```

This fallback can't work and should be deleted. Separately, once a bill closes the workflow
**completes**, so this endpoint stops working after Temporal retention expires — worth
documenting, or serve closed-bill state from Postgres.

---

## C. Money / FX precision (the challenge's explicit "type to store money" question)

The storage answer is **correct and well-justified** (`int64` minor units; the README's
float/string trade-off discussion is exactly what's being asked for). But the conversion
path quietly reintroduces float:

`int64 minor → float64 (× rate) → math.Round → int64`

`fx_rates.rate` is `NUMERIC(18,8)` in the DB but read into `float64` and multiplied in
`float64`. For normal amounts this is fine; for large amounts (>2^53 minor units) it loses
integer precision, and round-trip USD↔GEL isn't guaranteed to reconcile. A staff-level
answer would note this and either use `shopspring/decimal` or integer/rational math for the
conversion. Also: per-line rounding (vs rounding the total) is a real accounting decision
that should be stated explicitly. Minor: `fxConversionResult.RateDate` stores only the
*last* leg's date in triangulation, which is lossy for EUR→GEL.

---

## D. Security & operations

- **Public endpoints are unprotected.** Rate limiting and brute-force checks live *inside*
  the auth handler, so `POST /customers` (public) has no throttle → customer-creation spam
  and email-enumeration via the `AlreadyExists` response.
- **`rate_limit_entries` grows unbounded.** A row per `(customer, window)` and per `bf:`
  hash, forever. No TTL/janitor cron. Same unbounded-growth concern for `audit_log`.
- **API-key hashing:** plain unsalted `SHA-256` is actually *acceptable* here because keys
  are 128-bit random tokens (bcrypt unnecessary). Reasonable choice — though an HMAC with a
  server-side pepper would harden against DB-only compromise. Worth a sentence
  acknowledging the reasoning.
- **`audit_log.ip_address` is never populated**, and billing mutations (add/close) aren't
  audited — only auth events are. The system leans on Temporal history for the money audit
  trail, which is fine, but then drop the half-used column or finish it.
- **`/currencies` and `/fx/seed` are `private`, not "admin only."** `private` in Encore
  means *not internet-exposed*; anyone inside the service mesh can call them with no authz.
  The README's "admin only" is misleading.

---

## E. Code quality

- **Hand-rolled string utilities.** `isUniqueViolation` does
  `strings.Contains(err.Error(), "unique"/"duplicate")` — fragile; use pgx's
  `*pgconn.PgError` with `Code == "23505"`. And `contains`/`containsStr` reimplement
  `strings.Contains` for no reason — delete them.
- **`isErr` compares `err.Error()` strings.** Brittle and defeats `errors.Is`/wrapping. It
  exists because sentinels lose identity crossing the Temporal boundary (serialized to
  strings) — a real problem, but the right fix is typed/coded application errors (e.g.,
  `temporal.NewApplicationError` with a type), not string equality. Note that non-sentinel
  validation errors like `fmt.Errorf("description is required")` fall through to `Internal`
  (500) rather than `InvalidArgument` (400).
- `getAuthCustomerID` discards the `ok` from `auth.UserID()` — harmless on auth-only
  endpoints, but a footgun if reused.
- `listBills` (all-customers) is only used by tests and bypasses customer isolation — keep
  it test-scoped/clearly marked.
- No `Shutdown` on the Encore `Service` to drain workers gracefully.

---

## F. Testing

Strengths: the Temporal test-suite coverage (signals, auto-close, multi-item accumulation,
live query, already-ended-period) is genuinely good, and the repository integration tests
cover FX exact/fallback/too-old, idempotent close, and cross-currency math.

Gaps a staff reviewer would want closed:

- **No tests for the authorization layer** — `authorizeBillAccess` cross-customer denial is
  the security-critical path and is completely untested. For a multi-tenant billing API
  this is the most important test missing.
- No tests for the auth handler, rate limiting, or brute-force logic.
- No test asserting the API *rejects* an add to a closed bill (the requirement-#4
  behavior) — and indeed such a test would expose A1.
- No FX cron workflow test.
- Some integration tests assert on shared DB state (`GreaterOrEqual(2)`), which is
  order/run-dependent and flaky.

---

## G. Documentation

The README and ARCHITECTURE.md are clear and genuinely useful. Fixes:

- ARCHITECTURE.md says `temporalite start`; README says `temporal server start-dev`. Pick
  one (the latter is current; temporalite is archived).
- README: "all mutations flow through the workflow as signals" — false for create (A3).
- "admin only" for private endpoints (D).

---

## Strengths (credit where due)

- Correct money modeling with a documented rationale that directly answers the prompt's
  question.
- Deterministic workflow: `workflow.Now`, durable `Sleep` timer, selector loop, proper
  auto-close, and idempotent DB close.
- Correct DB concurrency: `SELECT ... FOR UPDATE` + transactions for add and close.
- Cross-currency via USD triangulation with previous-day fallback and *atomic* currency
  registration (all external calls before the transaction) — thoughtful and well beyond
  scope.
- Parameterized SQL throughout; clean, consistent package decomposition.
- Real Temporal test-suite usage rather than just hitting endpoints.

---

## Prioritized recommendations

1. **Make the money path correct first.** Add idempotency keys (line items, create) with
   `UNIQUE` + `ON CONFLICT` (B1); stop swallowing activity errors (B2).
2. **Fix API semantics.** Move add/close to **Temporal Update** with a validator so
   closed-bill rejection is synchronous and truthful (#4), and close returns total + line
   items (#3) — reserve a *signal* for the month-end collect (B/A1/A2).
3. **Harden the Temporal client** (retry connect, no cached-error `sync.Once`, no
   `log.Fatalf` in a goroutine) and add create/start compensation (B3, B4).
4. **Reconcile the narrative** — either genuinely make Temporal authoritative, or state
   plainly that Postgres is the system of record and Temporal owns sequencing + the durable
   timer (A3).
5. **Close the security/test gaps:** rate-limit public endpoints, add a janitor for
   rate-limit/audit rows, and add tests for cross-customer authorization and closed-bill
   rejection (D, F).
6. **Tidy quality items:** pgconn error codes, drop hand-rolled `contains`, typed/coded
   errors across the Temporal boundary, delete the dead query fallback (E, B5).

Net: strong engineering instincts and impressive scope, but the submission optimized for
feature breadth over the correctness and honest semantics of the core billing flow — which
is exactly what this challenge is probing. Tightening items 1–4 would move it from "good
senior" to "clearly staff."
