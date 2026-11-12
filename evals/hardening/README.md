# Hardening evaluation

A scenario-based eval of content generation and regeneration, localization, behavioral grouping and personalization, run against real Claude. The scenarios and their expectations live in [backend/internal/content/hardening_eval_test.go](../../backend/internal/content/hardening_eval_test.go). The expectations were written before the first run; don't edit them to make a scenario pass.

    cd backend && HARDEN_EVAL=1 ANTHROPIC_API_KEY=... go test ./internal/content -run TestHardeningEval -v -count=1
    # subset: HARDEN_EVAL_ONLY=G08,B02

Each full run overwrites `reports/latest.md`, the report to review, and `reports/latest.json`, the raw outputs. A filtered run (`HARDEN_EVAL_ONLY`) writes `reports/subset.{md,json}` instead, so it never replaces the full report and its manual review.

Behavioral group content scenarios (C01–C04) live in [group_content_eval_test.go](../../backend/internal/content/group_content_eval_test.go). Each one writes every group's messages the way the service does, then applies the same differentiation and claim checks the UI shows. The "Manual review" and "Recommendations" sections are written by hand after a run.
