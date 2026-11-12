# Hardening evaluation report

Generated 2026-11-12 11:23 by `TestHardeningEval` ([hardening_eval_test.go](../../../backend/internal/content/hardening_eval_test.go)). Expectations were written before execution.

## Overview

| Total | PASS | FAIL | REVIEW | NOT RUN |
|---|---|---|---|---|
| 5 | 4 | 0 | 1 | 0 |

## Results

| ID | Scenario | Expected | Actual | Status | Explanation |
|---|---|---|---|---|---|
| B03 | Overlapping behaviors | Groups (if any) validate, are ordered so the overlap is resolved intentionally, and descriptions are accurate under first-match assignment. | outcome=groups; Three behavioral groups separate lapsed, cart-active and recently active repeat customers, each needing a different call to action for the l…; groups: Lapsed repeat buyers [order count ≥ 2; last order more than 90 days ago] angle: Welcome them … | **REVIEW** | Read descriptions vs. first-match order. Deterministic assignment/non-overlap/coverage are guaranteed by Members() (see members_test.go) and need Postgres: NOT RUN here. |
| C01 | Lapsed electronics buyers (reported case) | Four clearly different messages: cart reminder only for the cart group, a valued-customer tone for high spenders (no cart), rediscovery of what's new for the long dormant, a plain general message for everyone else. No offers invented. | Recent cart intent: {{first_name}}, sepetinizdeki elektronik ürünler sizi bekliyor. Satın almayı tamamlamak ister misiniz? / High-value lapsed buyers: {{first_name}}, değerli müşterimiz olarak sizi yeniden ağırlamak isteriz. Güncel elektronik gamımızı keşfetme… | **PASS** |  |
| C02 | Offer campaign: buyers vs never-ordered carters | Offer and code kept exactly in every group; repeat buyers thanked as regulars, one-time buyers given a reason to come back, never-ordered carters invited to make a first order (never welcomed back). | Repeat buyers: {{first_name}}, thanks for coming back again and again. Take 30% off everything with code SPRING30 until Sunda… / One-time buyers: {{first_name}}, you've ordered with us once, so pick your next one: 30% off everything with code SPRING30 unti… / … | **PASS** |  |
| C03 | Overlapping behaviors resolved by order | Group 2 must not imply high value (those were taken by group 1); group 3 must not mention a cart; three different angles. | High spenders with cart: {{first_name}}, the items in your cart would already earn loyalty points. See your potential points on our sit… / Recent carters: {{first_name}}, finish the order you started and earn your first loyalty points. / High spenders, no cart… | **PASS** |  |
| C04 | Weakly differentiated groups | The system recognizes the weak split: at least one group reported as not differentiated, or flagged as nearly the same as the other. No invented differences. | Spend 3000+: {{first_name}}, our autumn collection is here. Take a look. / Spend 2500-3000: {{first_name}}, our autumn collection is here. Take a look. | **PASS** |  |

## Key failures


## Recommendations

_Filled in by hand after reviewing this run (see below)._

## Execution summary

- Model: `claude-haiku-5-5`
- Tokens: 29953 input, 9385 output (cost at list price: $0.0077)
- Duration: 57s
- Raw outputs: [latest.json](latest.json)
