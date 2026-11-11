# Smart Campaign Composer

An AI-powered campaign composer (case study). Marketers will describe a campaign in natural language, Claude will
propose audience rules and SMS/email copy, and a human approves before simulated delivery. All customer data is
synthetic and no real messages are sent. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md).

## Status

**Built:** schema and deterministic seed data, a read-only Audience Explorer, Claude audience suggestions with
backend validation, streamed SMS/email drafts (unlimited, editable, regenerated, duplicated or written by hand),
optional A/B tests of 2–3 drafts, automatic localization by recipient language, Behavioral-group and Individual AI
personalization with cost estimates, a deterministic consistency gate and explicit approval. Without
`ANTHROPIC_API_KEY`, deterministic mock generators are used.

**Not built yet:** the delivery queue and mock providers (rate limiting, retries, idempotency), delivery monitoring and
a metrics endpoint, a campaign list, `DECISIONS.md`, and `ai-sessions/`. Approving a campaign does not send anything.

## Run

Requires Docker with Compose. No `.env` is needed; copy `.env.example` to `.env` to override defaults.

```sh
make dev       # hot reload: Go (air) + Vue (Vite HMR) + Postgres, foreground  -> http://localhost:5173
make up        # production-like: compiled API + nginx-served frontend, detached -> http://localhost:5173
make down      # stop and remove containers (the database volume is kept)
make test      # Go tests and frontend type-check, run in containers (starts Postgres for the DB tests)
```

Use one of `make dev` / `make up` at a time (both use port 5173). The API is also on http://localhost:8080 and
Postgres on 5432.

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
| `GET …/individual/candidates`, `POST …/individual/sample`, `GET …/individual/estimate`, `POST …/individual/apply`, `POST …/individual/stop` | Individual personalization: sample, estimate, explicit Apply to all (background, 4 concurrent calls), stop/resume |
| `GET /api/campaigns/{id}/review?version=` | Consistency gate, recipient plan (per variant/group/language/channel/source) and AI cost |
| `POST /api/campaigns/{id}/approve` | `{version}` (Standard draft; 0 with variants): re-checks audience and gate, then freezes a snapshot (`409` once approved, `422` with `issues` if invalid). Sends nothing |

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
  review screen's plan uses it, and the `deliveries` table (with `delivery.Breakdown`) is the contract for the upcoming
  delivery pipeline and monitoring.

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
backend/internal/db/        embedded schema.sql
backend/internal/seed/      deterministic synthetic data
backend/internal/customers/ customer and audience queries (pgx, parameterized SQL)
backend/internal/llm/       shared Anthropic client setup and error mapping
backend/internal/segmentation/ AI audience rules: generation, validation, compilation to SQL
backend/internal/content/   streamed message drafts, validation, drafts and approval
backend/internal/dbtest/    isolated-schema helper for database tests
frontend/                   Vue 3 + TypeScript + Vite dashboard
```

## Known limitations

- The schema is one idempotent file, not versioned migrations; changing an existing table needs a real migration step.
- Consent is a boolean per channel, with no timestamp or audit history.
- Seed phone numbers are random Turkish mobile-format numbers and could coincide with real ones; nothing is ever sent.
- SMS length is counted in Unicode characters; GSM-7 vs. UCS-2 encoding (e.g. Turkish letters) and multi-part SMS are not modelled.
- Campaigns are stored, but there is no list or URL to resume one after a page reload yet.
- Only one generation per campaign runs at a time, and group/individual generation runs in-process; both are enforced
  in memory (single API instance). After a restart, “Resume” continues pending individual messages.
- No delivery worker or monitoring dashboard yet: the deliveries table and breakdown query are the contract for them.
  Monitoring will report delivery performance only (no opens, clicks, conversions or automatic winner selection).
- The audience–message consistency checks are a few keyword rules (first purchase / welcome back) and only warn.
- The invented-facts check is a heuristic warning, not a guarantee.
# smart-campaign-composer
