# Decisions

Design decisions for delivery and monitoring, and what they guarantee. Code: `backend/internal/delivery/`,
`backend/cmd/worker/`, `backend/internal/content/approved.go`.

## Architecture

```
Vue ──HTTP──▶ API (cmd/api) ──┐                    ┌── Worker (cmd/worker) ──▶ mock SMS / email provider
  ▲   Send, poll /delivery    │ approve, dispatch  │ claim, send, record
  │                           ▼                    ▼
  └──────────── reads ── PostgreSQL: deliveries (= the queue), campaigns, mock_provider_messages, worker_heartbeats
```

- **Separate worker process, same Go module and image.** Sending is long-running, rate-limited I/O and shouldn't share
  a process, or a restart, with request handling. The API and the worker talk only through PostgreSQL. There's no
  internal HTTP and no broker.
- **PostgreSQL is the queue.** The `deliveries` row is both the job and the record shown in monitoring, so there's one
  source of truth and nothing to keep in sync. At this scale (thousands of messages, 200/s total) row locking is plenty.
  Redis or RabbitMQ would add an extra system plus a consistency problem between the queue and the records.
- **The worker is narrow.** A job holds the final subject and body. The worker doesn't know about segmentation, A/B
  tests, groups or personalization. It re-checks consent, rate-limits, calls the provider and records the result.

## Approval → Send

Approval and sending are separate, explicit actions. `POST /send` runs `delivery.Dispatch` in one transaction:

1. `SELECT … FROM campaigns WHERE id = $1 FOR UPDATE`. Concurrent Send clicks serialize here.
2. If the campaign isn't approved, return `409`. If `dispatched_at` is already set, return the current state. A batch is
   never recreated or restarted.
3. Resolve messages with `content.ApprovedMessages`. Membership and channel consent are read **now**, and the content
   comes from the **approval snapshot** (`approved_snapshot`: drafts, variants, group texts). Individual messages are
   read from `personalized_messages`, which can't change after approval. This reuses the same `resolver` the review
   plan uses, so A/B assignment (`variantFor`, a hash of campaign and customer), language fallback, group and
   individual content, and `{{first_name}}` rendering are identical to what was approved.
4. Bulk insert the jobs (`INSERT … SELECT FROM unnest(…) ON CONFLICT DO NOTHING`), then set `dispatched_at`, then commit.

The API returns `202` right away. Variant, group, language and content source are stored on each job and never
change on retry, so breakdowns stay exact.

## Queue and worker

- **Claim**: a single autocommit statement, `UPDATE … WHERE id IN (SELECT … FOR UPDATE SKIP LOCKED LIMIT n)`, sets
  `status = 'sending'` and a lease (`locked_until`, 30s). Concurrent claimers take disjoint rows. No transaction is
  open while the provider is called.
- **Crash recovery**: a `sending` row whose lease has expired is due again. That attempt is *resumed* with the same
  idempotency key and isn't counted twice. On shutdown the worker finishes in-flight calls. Jobs it claimed but
  hadn't started wait for their lease to expire (≤30s).
- **Outcome write**: `UPDATE … WHERE id = $1 AND status = 'sending'`, so a late write after the job finished
  elsewhere is ignored.
- **Per channel**: one claim loop plus 10 sender goroutines for SMS, and the same for email.
- **Heartbeat**: `worker_heartbeats` is upserted every 5s. Docker's healthcheck runs `worker -healthcheck`, and
  monitoring warns when work is pending but the heartbeat is older than 20s.

## Rate limiting

**At most 100 provider calls per second per channel**, retries included. The limiter is a sliding window over the
last 100 call times: a call waits until the oldest of the last 100 is a full second old. SMS and email have
independent limiters. Concurrency (10 per channel) only sets throughput; the limiter is the guarantee. A provider 429
reschedules that message (see below) and doesn't slow the whole channel.

The limit is enforced inside one worker process, and the deployment runs exactly one worker replica. Running several
replicas would need a shared limiter, such as a token bucket in PostgreSQL or Redis. That wasn't built because the
deployment doesn't need it.

## Retry policy

| Provider result | Action |
|---|---|
| 2xx | `sent` |
| 429 | retry after `Retry-After` (capped at 60s), else exponential backoff |
| 5xx, timeout, no response | retry with backoff 2s · 2^(n−1) (2s, 4s, 8s), ±20% jitter, capped at 30s |
| other 4xx | `failed`, no retry |
| attempt 4 still failing | `failed`: "gave up after 4 attempts: …" |
| consent withdrawn / customer deleted | `skipped`, no provider call, no retry |
| no consent for an enabled channel at Send | `skipped` when the jobs are created ("not eligible: no SMS consent") |

Attempts, last error and `next_attempt_at` are columns, so retries survive restarts. Every provider call is also a
row in `delivery_attempts` (attempt number, HTTP status or none, provider code, latency, outcome), written in the same
statement as the delivery update. A rejection is flagged `permanent`.

**Manual retry.** A marketer can requeue failed deliveries that aren't permanent. The same row goes back to
`retrying`, so the same idempotency key, worker, rate limit and consent re-check apply. `retry_base` records the attempt
count at that moment, so the policy gives a new round of up to 4 attempts. The first attempt of the round is marked
`manual` in the history. Sent, skipped, pending and permanently rejected deliveries are refused with a reason. A
message that the provider accepted earlier but whose response was lost is replayed under its key and gets the
original message ID, so nothing is sent twice. A retry is a row becoming due
again, never a tight loop. Error strings are stable so monitoring can group them into "common reasons".

## Idempotency: what is and isn't guaranteed

- **Enqueue at most once.** The campaign row lock plus `dispatched_at` make Send idempotent, and
  `UNIQUE (campaign_id, customer_id, channel)` is the backstop. Repeated clicks, retried requests and concurrent Sends
  can't create a second job for a recipient.
- **Stable idempotency key.** `campaign-{id}/customer-{id}/{channel}`, the same identity as the unique constraint. It's
  sent on every attempt, including after an ambiguous result (timeout or lost response) and after a crash. A new key is
  never generated.
- **The mock provider honors the key durably.** It records accepted keys in `mock_provider_messages`, its "server
  side", whose primary key accepts each key once. A replay returns the original message ID and sends nothing new. Tests
  verify that a lost response followed by a retry produces exactly one accepted message.
- **The limit of the guarantee.** A database constraint can't make an external call exactly-once. If a provider doesn't
  support idempotency keys, a crash or timeout between "provider accepted" and "we recorded it" can still cause a
  duplicate on retry. With a key-honoring provider (as mocked here), the system is effectively exactly-once. Without
  one it's at-least-once for that narrow window.

## Content safety

- Consent is checked when jobs are created and again right before each provider call. Withdrawn consent gives
  `skipped` with a reason, and the message isn't retried.
- Jobs are never re-resolved. The worker can't substitute another variant, language or text.
- Draft changes can't reach queued messages. Approved campaigns are immutable, and jobs carry their own copy of the
  text. A test edits the drafts table directly after approval and checks that the sent content doesn't change.

## Monitoring

- `GET /api/campaigns/{id}/delivery` aggregates the `deliveries` rows: status, totals, SMS and email, A/B variant,
  behavioral group, retries, top error reasons, next retry and worker heartbeat. Every strategy uses the same pipeline
  and the same report. Individual generation progress lives in the composer, and delivery progress lives here.
- Vue polls once a second while messages are pending and stops when everything has finished. The state is all in
  PostgreSQL, so a refresh or worker restart loses nothing. Polling is simpler than WebSockets, and at this refresh
  rate it's enough.
- `GET /api/metrics` gives global queue and delivery statistics. There's no Prometheus, Grafana or OpenTelemetry.
- "Sent" means accepted by the provider. Delivery receipts, opens, clicks and conversions aren't simulated.

## Known limitations

- One worker replica (see Rate limiting). Claims are safe with several workers, but the rate limit isn't.
- Mock behavior is configurable (`MOCK_FAULT_RATES`, `MOCK_LATENCY_MS`, `MOCK_FAULT_SEED`, `MOCK_PROVIDER_FAULTS=off`).
  Fixed recipient failures depend on an address hash, so roughly 2–4% of addresses per channel fail by design.
- No cancel/pause of a sending campaign and no scheduled sends. Manual retry covers temporary failures only.
- Polling at 1s. The report runs a few aggregate queries per poll, which is fine for one marketer per campaign.
- Jobs claimed but not started at shutdown wait for their lease (≤30s) before another worker takes them.
- The schema is an idempotent file. The status check on `deliveries` is replaced in place on startup, and migrations
  from the API and the worker are serialized with an advisory lock.
