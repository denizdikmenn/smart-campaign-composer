# Smart Campaign Composer

> New here, or not technical? Start with the [product overview](PRODUCT.md).

An AI-powered campaign composer (case study). Marketers describe a campaign in natural language, Claude
proposes audience rules and SMS/email copy, a human approves, and the campaign is sent through mock providers. All customer data is
synthetic and no real messages are sent.

## Status

**Built:** schema and deterministic seed data, a read-only Audience Explorer, Claude audience suggestions with
backend validation, streamed SMS/email drafts (unlimited, editable, regenerated, duplicated or written by hand),
optional A/B tests of 2–3 drafts, automatic localization by recipient language, Behavioral-group and Individual AI
personalization with cost estimates, a deterministic consistency gate and explicit approval, then an explicit Send
(now or scheduled): a separate delivery worker sends through rate-limited mock SMS/email providers with retries and
idempotency, and a live monitoring page shows progress by channel, variant and group. Draft campaigns can be resumed
from the Campaigns list. Without `ANTHROPIC_API_KEY`, deterministic mock generators are used.

**Not built:** a CI pipeline, frontend unit tests, pausing or cancelling a campaign that is already sending, and
delivery receipts (opens, clicks). See [Known limitations](#known-limitations).

## Run

Requires Docker with Compose. No `.env` is needed; copy `.env.example` to `.env` to override defaults.

```sh
make dev       # hot reload: Go (air) + Vue (Vite HMR) + Postgres, foreground  -> http://localhost:5173
make up        # production-like: compiled API + nginx-served frontend, detached -> http://localhost:5173
make down      # stop and remove containers (the database volume is kept)
make test      # Go tests and frontend type-check, run in containers (starts Postgres for the DB tests)
```

Use one of `make dev` / `make up` at a time (both use port 5173). The API is also on http://localhost:8080 and
Postgres on 5432. Both start four services: `postgres`, `backend` (API), `worker` (delivery, no port) and `frontend`.

Worker settings (environment):

| Variable | Default | Effect |
|---|---|---|
| `DELIVERY_RATE_PER_SECOND` | 100 | Provider calls per second, per channel |
| `DELIVERY_CONCURRENCY` | 10 | Sends in flight per channel |
| `MOCK_LATENCY_MS` | `80-400` | Simulated provider latency range |
| `MOCK_FAULT_RATES` | `429=0.06,5xx=0.1,4xx=0.02,lost=0.03` | Random per-call faults: rate limit, server error, permanent rejection, lost response |
| `MOCK_FAULT_SEED` | random | Reproduces a run's random faults |
| `MOCK_PROVIDER_FAULTS` | `on` | `off` disables all simulated failures |

Besides the random faults, a few recipients always fail the same way. The rule is a hash of the address into 100
buckets, so it is reproducible across campaigns:

| Channel | Bucket | Response | Kind |
|---|---|---|---|
| SMS | 0 | `400 SMS_INVALID_NUMBER` | permanent |
| SMS | 1 | `403 SMS_BLOCKED_BY_CARRIER` | permanent |
| SMS | 2 | `503 SMS_CARRIER_UNAVAILABLE` on the first 2 calls | transient, then sent |
| Email | 0 | `400 EMAIL_INVALID_ADDRESS` | permanent |
| Email | 1 | `422 EMAIL_SUPPRESSED` | permanent |
| Email | 2 | `503 EMAIL_MAILBOX_BUSY` on the first call | transient, then sent |
| Email | 3 | `503 EMAIL_MAILBOX_BUSY` on every call | exhausts its 4 attempts; retryable by hand |

### Sending

After approval, **Continue to sending** opens `#/campaigns/{id}/delivery`. It shows what Send will queue,
recomputed now from the approved content and current consent. Sending takes an explicit **Send campaign** and a
confirm. The API creates one job per recipient and channel and returns at once. The worker sends the jobs, and the
page polls progress once a second until every message is finished. The page and its numbers come from PostgreSQL, so
they survive refreshes and worker restarts. Approved campaigns are listed on the Campaigns page.

Instead of sending now, the campaign can be **scheduled** for a later time (shown in the browser's time zone, stored
in UTC) and the schedule changed or cancelled until it starts. The worker checks for due campaigns every 5 seconds
and dispatches them exactly like Send, with the audience and consent read at that moment. If nobody is reachable by
then, the reason is shown on the page instead of being retried.

The page also lists every message (recipient, channel, status, attempts, last error) with filters and paging. **History**
shows each provider call: time, latency, HTTP status, code and outcome. **Retry** requeues one message that failed
temporarily, and **Retry N failed** requeues all of them. A retry reuses the same delivery record and idempotency key,
starts a new round of up to 4 attempts, and is checked for consent again. In this demo a manually retried message isn't
subjected to simulated failures, so it goes out. Permanent rejections, already accepted
messages and skips can't be retried. Audience members who haven't consented to an enabled channel are recorded as
**skipped** ("not eligible: no SMS consent") rather than silently left out.

## API

JSON unless noted.

| Endpoint | Purpose |
|---|---|
| `GET /api/health`, `GET /api/ready` | Liveness; readiness (checks the database) |
| `GET /api/customers/summary` | Totals, per-channel consent, buyers, repeat buyers, 30-day active, average order value, cities |
| `GET /api/audiences?category=` | The six audiences with size and email/SMS-eligible counts |
| `GET /api/customers` | Paginated customers with order insights. Filters: `page`, `page_size` (max 100), `city`, `email_consent`, `sms_consent` (`true`/`false`), `segment`, `category` |
| `POST /api/segmentation/suggest`, `POST /api/segmentation/preview` | AI audience alternatives for a brief; sample customers for audience rules |
| `POST /api/campaigns` | Create a campaign from `{brief, audience: {name, match, conditions}}`. Rules are re-validated and recounted on the server |
| `GET /api/campaigns/{id}` | Campaign, all drafts (each with validation `issues`) and the allowed setting values |
| `POST /api/campaigns/{id}/generate` | `{from_version, feedback}` → **server-sent events**: `field` `{field, value}` as Claude writes, then `done` `{draft}` or `error` `{message, retryable}`. `from_version: 0` writes Draft 1; otherwise that draft (with edits and settings) is revised into a new version |
| `PUT /api/campaigns/{id}/drafts/{version}` | Save edits (channels, settings, SMS body, email subject/body); returns the draft with its issues |
| `POST /api/campaigns/{id}/drafts` | `{from_version}` duplicates that draft; `{}` creates a blank draft |
| `POST /api/campaigns/{id}/drafts/{v}/localize` | (Re)translates missing or out-of-date language versions |
| `PUT /api/campaigns/{id}/variants` | `{versions: [a, b, c]}` sets 2–3 drafts as Variants A–C; `[]` ends the A/B test |
| `PUT /api/campaigns/{id}/personalization` | `{mode: standard\|groups\|individual, base_version}` |
| `POST …/groups/propose`, `GET …/groups/estimate`, `POST …/groups/generate`, `GET …/groups/{pos}/examples`, `PUT …/groups/{pos}` | Behavioral groups: propose (1 AI call), cost estimate, background generation (one call per group), representative members, edits |
| `GET …/individual/candidates`, `POST …/individual/sample`, `GET …/individual/estimate`, `POST …/individual/apply`, `POST …/individual/stop` | Individual personalization, capped at 10 customers in this demo (samples included; the most active customers with history). Everyone else gets the base draft, and picks that weren't personalized show it too. Sample, estimate, explicit confirm (background, 4 concurrent calls), stop/resume |
| `GET /api/campaigns/{id}/review?version=` | Consistency gate, recipient plan (per variant/group/language/channel/source) and AI cost |
| `POST /api/campaigns/{id}/approve` | `{version}` (Standard draft; 0 with variants): re-checks audience and gate, then freezes a snapshot (`409` once approved, `422` with `issues` if invalid). Sends nothing |
| `GET /api/campaigns` | The 20 most recent campaigns with approval and delivery status |
| `POST /api/campaigns/{id}/send` | Queues the approved campaign once (`202` + delivery report). Idempotent: repeating it returns the current state. `409` if not approved or nobody is reachable |
| `POST /api/campaigns/{id}/schedule`, `DELETE /api/campaigns/{id}/schedule` | `{send_at}` (RFC 3339, at least a minute ahead) schedules or reschedules the send; `DELETE` cancels it. `409` once sending has started. Both return the delivery report |
| `GET /api/campaigns/{id}/delivery` | Delivery report: status (`not_sent`, `scheduled`, `queued`, `sending`, `completed`, `completed_with_failures`), totals, per-channel/variant/group counts, retries, top error reasons, next retry, worker heartbeat |
| `GET /api/campaigns/{id}/messages?status=&channel=&page=&page_size=` | Recipient-level deliveries (status: `pending`, `retrying`, `sent`, `failed`, `skipped`; max 100 per page) |
| `GET /api/campaigns/{id}/messages/{mid}` | One delivery with its content and attempt history |
| `POST /api/campaigns/{id}/messages/{mid}/retry`, `POST /api/campaigns/{id}/retry-failed` | Requeue failed deliveries that weren't permanently rejected (`409` with the reason for a single message that can't be retried) |
| `GET /api/metrics` | Queue depth (due, scheduled retries, in flight, oldest due age), per-channel status counts, sent in the last minute, attempts, worker heartbeat |
| `GET /api/admin/segmentation/runs?status=&limit=`, `GET /api/admin/segmentation/runs/{id}` | Audience-generation traces (brief, raw model output, validation notes, tokens). Unauthenticated, like the rest of this demo |

Behavioral groups are planned with a **message angle** each: how the group's message should differ, grounded only in its rules. Each group's content call gets the brief, the base message, the planned angle, what's guaranteed for every member (audience and group rules), the earlier groups it excludes (first match wins), the group's actual behavioral profile (tendencies, never stated as facts) and the other groups' angles. It returns the messages plus a **rationale** shown on the group card ("Why this message"). It may answer that a group has no honest reason for a different message instead of inventing one. Deterministic warnings flag groups planned with the same angle, near-identical messages, a group that repeats the base message, and cart mentions a group's rules don't guarantee.

After approval, `GET …/review` returns the recipient plan of the approved content (the pre-send summary).

### Content rules

Enforced in Go (`backend/internal/content/validate.go`), not only in the prompt: SMS ≤ 160 characters *after*
substituting the longest first name among the audience's reachable members, email subject ≤ 80, required fields for
enabled channels, at least one channel, and `{{first_name}}` as the only variable. Discounts, amounts and code-like
tokens that are not in the brief produce a non-blocking warning. A generated draft that breaks a rule is kept but
cannot be approved until it is edited or regenerated; malformed model output is rejected and never stored. Every
generation is traced in `content_runs` (inputs, raw output, tokens, latency, status); the AI cost shown in the UI is
summed from these rows with the list prices in `internal/llm`.

### Drafts, variants and personalization

- **Drafts** are unlimited working versions (initial, regenerated, A/B candidate, manual, duplicate). Nothing overwrites
  or deletes a draft. **Variants** are drafts explicitly promoted into an A/B test (2–3). Customers are split equally
  by a hash of campaign and customer id: one variant per customer for every channel, stable across retries.
- **Localization** is automatic: drafts are written in the audience's most common language and translated into the
  other languages present. Supported content languages are Turkish and English; any other language receives English.
  The seed is ~75% Turkish, ~20% English (more in tourist cities) and ~3% German (to exercise the fallback), with
  names that fit each language.
- **Behavioral groups**: Claude proposes rules from aggregate audience data; the backend validates them with the
  audience-rule validator and assigns members in SQL (first match wins, the rest form “Everyone else”), so groups never
  overlap or lose anyone. One call per group writes all of that group's languages.
- **Individual**: one call per customer with only first name, city, order count and recent order/cart categories.
  Customers without history, and messages that fail validation or mention offers not in the brief, get the base draft.
- After the audience, the composer has three steps: Content (drafts), Strategy (single message, A/B test, behavioral
  groups or individual: one choice) and Review & approve. Switching strategy never deletes drafts.
- `content/resolve.go` resolves each recipient and channel to final text plus variant, group, language and source. The
  review plan and the delivery pipeline both use it, so what is sent matches what was reviewed.

## Data and audiences

On startup the API applies `backend/internal/db/schema.sql` (idempotent) and, if `customers` is empty, loads
`SEED_CUSTOMERS` (default 5000; `0` disables) customers with orders and cart events from `backend/internal/seed`.
The seed is deterministic for a given count and day, and builds histories from behavioural archetypes (VIPs, cart
abandoners, lapsed buyers, new customers, ...) so they are coherent. Emails use reserved `example.*` domains.

Audiences are fixed server-side definitions in `backend/internal/customers/audience.go`, expressed as parameterized
SQL. They can overlap, and email and SMS consent are counted separately:

| Audience | Definition |
|---|---|
| Abandoned carts | Added to cart in the last 30 days, no purchase since |
| Win-back | Bought before, last order over 90 days ago |
| VIP | 4+ orders and 5,000+ TRY lifetime spend |
| Category buyers | Bought the chosen category in the last 180 days |
| New customers | Joined in the last 30 days, no purchase |
| Inactive visitors | Never purchased, not active in the last 60 days |

The seed only runs on an empty table. After changing the schema or seed, reset the data:
`make down && docker volume rm smart-campaign-composer_postgres_data`.

## Layout

```
backend/cmd/api/            HTTP server, handlers, input validation
backend/cmd/worker/         delivery worker process (no HTTP port; `worker -healthcheck`)
backend/internal/db/        embedded schema.sql
backend/internal/seed/      deterministic synthetic data
backend/internal/customers/ customer and audience queries (pgx, parameterized SQL)
backend/internal/llm/       shared Anthropic client setup and error mapping
backend/internal/segmentation/ AI audience rules: generation, validation, compilation to SQL
backend/internal/content/   streamed message drafts, validation, drafts and approval; approved-content resolution
backend/internal/delivery/  queue (dispatch, claim, leases), worker, mock providers, retry policy, monitoring
backend/internal/dbtest/    isolated-schema helper for database tests
frontend/                   Vue 3 + TypeScript + Vite dashboard
```

## Known limitations

- The schema is one idempotent file, not versioned migrations; changing an existing table needs a real migration step.
- Consent is a boolean per channel, with no timestamp or audit history.
- Seed phone numbers are random Turkish mobile-format numbers and could coincide with real ones; nothing is ever sent.
- SMS length is counted in Unicode characters; GSM-7 vs. UCS-2 encoding (e.g. Turkish letters) and multi-part SMS are not modelled.
- Only one generation per campaign runs at a time, and group/individual generation runs in-process; both are enforced
  in memory (single API instance). After a restart, “Resume” continues pending individual messages.
- Delivery runs in one worker instance; the 100/s per-channel limit is enforced in that process. Mock providers report
  acceptance only, so monitoring shows "sent", never delivered, opened or clicked (no automatic A/B winner either).
- The audience–message consistency checks are a few keyword rules (first purchase / welcome back). They block approval
  only when the audience rules guarantee everyone has ordered before; otherwise they warn.
- The invented-facts check is a heuristic warning, not a guarantee.
- The mock providers run inside the worker process (no HTTP hop), and their simulated 429s are random rather than
  triggered by exceeding a provider-side limit.
- A scheduled send re-reads the audience when it starts; the SMS length check uses the longest first name at approval
  time, and rendered messages are not length-checked again at send time.
