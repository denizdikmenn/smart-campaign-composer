## Verification Discipline

- Keep verification proportional to the scope of changes.
- For backend changes, run relevant Go tests.
- For frontend changes, run typecheck and build.
- For database changes, verify relevant queries against PostgreSQL.
- Do not run repeated full test suites unless changes justify it.
- Do not perform browser automation, screenshot testing,
  or production deployment checks unless explicitly requested.
- Do not stop or restart running services solely for verification
  without asking.
- After checks pass, summarize results and stop.


## Development & Verification

- Assume the development environment is already running.
- Do not run `make up`, `make down`, `docker compose up/down`, or restart services unless explicitly requested.
- Never stop, kill, recreate, or clean up running containers or processes without permission.
- Do not rebuild Docker images for routine code changes. Go Air and Vue Vite already support hot reload.
- Run only checks relevant to the files changed:
  - Go: targeted `go test`
  - Vue: typecheck/build when necessary
  - Database: targeted queries when necessary
- Avoid repeated full test suites, browser automation, screenshots, and production-like verification unless requested.
- Never run destructive database operations or reset volumes without approval.
- If verification requires restarting infrastructure, report the limitation instead of doing it automatically.
- Once the requested changes and relevant checks are complete, summarize and stop.
