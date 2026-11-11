# Smart Campaign Composer

An AI-powered campaign composer (case study). Marketers will describe a campaign in natural language, Claude will
propose audience rules and SMS/email copy, and a human approves before simulated delivery. All customer data is
synthetic and no real messages are sent. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md).

## Status

**Built:** schema and deterministic seed data, a read-only Audience Explorer (API + Vue dashboard).

**Not built yet:** Claude integration (audience rules, SMS/email generation, streaming), campaign review and approval,
the delivery queue and mock providers (rate limiting, retries, idempotency), delivery monitoring and a metrics
endpoint, `DECISIONS.md`, and `ai-sessions/`.

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

All endpoints are read-only JSON.

| Endpoint | Purpose |
|---|---|
| `GET /api/health`, `GET /api/ready` | Liveness; readiness (checks the database) |
| `GET /api/customers/summary` | Totals, per-channel consent, buyers, repeat buyers, 30-day active, average order value, cities |
| `GET /api/audiences?category=` | The six audiences with size and email/SMS-eligible counts |
| `GET /api/customers` | Paginated customers with order insights. Filters: `page`, `page_size` (max 100), `city`, `email_consent`, `sms_consent` (`true`/`false`), `segment`, `category` |

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
backend/internal/dbtest/    isolated-schema helper for database tests
frontend/                   Vue 3 + TypeScript + Vite dashboard
```

## Known limitations

- The schema is one idempotent file, not versioned migrations; changing an existing table needs a real migration step.
- Consent is a boolean per channel, with no timestamp or audit history.
- Seed phone numbers are random Turkish mobile-format numbers and could coincide with real ones; nothing is ever sent.
# smart-campaign-composer
