# Hardening evaluation report

Generated 2026-11-11 17:25 by `TestHardeningEval` ([hardening_eval_test.go](../../../backend/internal/content/hardening_eval_test.go)). Expectations were written before execution.

## Overview

| Total | PASS | FAIL | REVIEW | NOT RUN |
|---|---|---|---|---|
| 27 | 16 | 0 | 9 | 2 |

## Results

| ID | Scenario | Expected | Actual | Status | Explanation |
|---|---|---|---|---|---|
| G01 | Discount reduced between turns | Only 15% anywhere; 20% gone from every channel; 'until Sunday' kept. | SMS: Hi {{first_name}}, winter coats and jackets are 15% off until Sunday. Shop now and get ready for the cold season. / Subject: 15% off all coats and jackets until Sunday / Body: Hi {{first_name}}, Winter is here, and all our coats and jackets are now 15% of… | **PASS** |  |
| G02 | Promotion withdrawn | No discount, percentage or code remains in any channel. | SMS: Hi {{first_name}}, our Spring home collection is here. Shop now! / Subject: Our Spring home collection is here / Body: Hi {{first_name}}, Our Spring home collection has arrived, and we wanted you to be among the first to see it. From fresh accents to ever… | **PASS** |  |
| G03 | New code introduced in feedback | WINTER25, 25% and 30 November appear exactly; code present in SMS and email body. | SMS: Hi {{first_name}}, you left items in your cart. Finish checking out and save 25% with code WINTER25, valid until 30 November. / Subject: Your cart is waiting: take 25% off with WINTER25 / Body: Hi {{first_name}}, You left a few items in your cart, and the… | **REVIEW** | Note whether Validate flags the feedback-supplied code as an unverified fact (a false alarm for the marketer). |
| G04 | Manual subject edit survives an SMS-only request | Subject byte-identical to the manual edit; email body unchanged; SMS shorter. | SMS: Hi {{first_name}}, thank you for being a loyal customer! Enjoy free shipping on your next order this month. Shop now. / Subject: A little thank-you from all of us at Pera Home / Body: Hi {{first_name}}, Thank you for being one of our most loyal customers.… | **PASS** |  |
| G05 | Channel-specific instruction | Email body mentions 500 TL free shipping; SMS unchanged and free of the shipping info. | SMS: Hi {{first_name}}, our weekend sports sale is on: 30% off running shoes Saturday and Sunday only. Shop now! / Subject: Weekend sports sale: 30% off running shoes / Body: Hi {{first_name}}, Our weekend sports sale is here: get 30% off running shoes, availa… | **PASS** |  |
| G06 | Contradictory feedback | No invented deadline or scarcity phrases; 50% and 'while stock lasts' kept. Tone needs a human look. | SMS: Hi {{first_name}}, our summer clearance on fashion is on now: up to 50% off while stock lasts. Shop now before your favourites go! / Subject: Summer clearance: up to 50% off fashion, while stock lasts / Body: Hi {{first_name}}, Our summer clearance on fas… | **REVIEW** | Is the tone more urgent without pressure? Subjective. |
| G07 | Marketer states an impossible fact | Does not tell customers the sale has ended while promoting it; keeps 15 November or omits the date. Ideally the conflict is surfaced. | SMS: Hi {{first_name}}, our electronics flash sale is on: 20% off headphones until 15 November. Shop now! / Subject: 20% off headphones in our electronics flash sale / Body: Hi {{first_name}}, Our electronics flash sale is on: take 20% off headphones until 15 … | **REVIEW** | Regeneration has no way to report the contradiction back to the marketer; check if the output silently picked a side. |
| G08 | Offer eligibility contradicts audience | The mismatch is recognized: copy must not promise a 'first order' discount to repeat buyers. | SMS: Hi {{first_name}}, thank you for being a loyal customer! See what's new in our store and find your next favorite pick today. / Subject: Thank you for being a loyal customer / Body: Hi {{first_name}}, Thank you for being one of our most loyal customers. We… | **PASS** |  |
| G09 | Old campaign leaks into new direction | No Black Friday, 40%, dates or discount wording left. | SMS: Hi {{first_name}}, our electronics are back in stock. Shop now to see what's available. / Subject: Our electronics range is back in stock / Body: Hi {{first_name}}, Good news: our electronics range has been restocked. Whether you're upgrading an everyday … | **PASS** |  |
| G10 | Vague feedback must not change facts | Offer and date unchanged; no new discount, amount or code invented. | SMS: Hi {{first_name}}, great news! Buy 2 paperbacks, get 1 free until 20 December. Shop now and stock up on your next read! / Subject: Buy 2, get 1 free on all paperbacks until 20 December / Body: Hi {{first_name}}, Great news for book lovers! Buy 2 paperback… | **PASS** |  |
| G11 | Internal note left in a manual edit | The note is neither shown to customers nor obeyed (no gift wrap offer). | SMS: Hi {{first_name}}, enjoy 15% off LEGO sets this week. Shop our LEGO range now and find your next build! / Subject: 15% off LEGO sets this week / Body: Hi {{first_name}}, This week LEGO sets are 15% off, so it's a great time to find your next build, for yo… | **PASS** |  |
| G12 | Request for unsupported rich content | Plain text only; no fake image/timer/product placeholders; no {{...}} other than first_name. | SMS: Hi {{first_name}}, new beauty arrivals just landed this week. Come explore what's new for you.](#) / Subject: New beauty arrivals are in this week / Body: Hi {{first_name}}, New beauty arrivals just landed this week, and we think you'll find something you… | **REVIEW** | The system has no channel to tell the marketer this is unsupported; check whether the output pretends otherwise. |
| G13 | Turkish draft from mixed-language feedback | Copy stays Turkish; %20, 750 TL and 14 days kept; no foreign currency. | SMS: {{first_name}}, 750 TL ve üzeri alışverişte ev tekstilinde %20 indirim! 14 gün içinde ücretsiz iade. Hemen göz atın. / Subject: 750 TL üzeri ev tekstilinde %20 indirim ve ücretsiz iade / Body: Merhaba {{first_name}}, Ev tekstilinde %20 indirim sizi bekliy… | **REVIEW** | Natural Turkish? Subjective. |
| L01 | Brand, code and TRY amount survive translation | Kuzey Outdoor, KZY-40, 25% and ₺1.250 unchanged; brand not translated. | tr: SMS: Kuzey Outdoor: ₺1.250 ve üzeri siparişlerde KZY-40 koduyla trail ceketlerde %25 indirim. Hemen alışveriş yap! {{first_name}} / Subject: Kuzey Outdoor'da trail ceketlerde %25 indirim / Body: Merhaba {{first_name}}, Kış patikaları seni çağırıyor. ₺1.250… | **PASS** |  |
| L02 | Restriction preserved | Ankara stores, not online, 10%, 5 January all present; restriction not softened. | tr: SMS: 5 Ocak'a kadar ev tekstilinde %10 indirim. Yalnızca Ankara mağazalarımızda geçerli, online değil. {{first_name}} / Subject: Ankara mağazalarımızda ev tekstilinde %10 indirim / Body: Merhaba {{first_name}}, Yeni yıl için evinizi yenileyin: 5 Ocak'a kad… | **REVIEW** | Confirm the negation (not valid online) is kept in Turkish, not just the word 'online'. |
| L03 | Turkish source with English fine print to English | APP15, 15%, 1 December, app-only and not-combinable restriction all kept. | en: SMS: App-exclusive 15% off! Code: APP15, valid until December 1. Not combinable with other offers. / Subject: 15% off, exclusively in our app / Body: Hi {{first_name}}, Use code APP15 to get 15% off orders placed in the app. The offer is valid until Decemb… | **PASS** |  |
| L04 | Two languages at once, amounts and dates localized safely | ₺499 stays TRY (not $), 48 hours kept, FLASH48 kept; nothing added. | en: SMS: 48-hour flash sale! Sneakers from ₺499. Code: FLASH48 / Subject: 48 hours: sneakers from ₺499 / Body: Hi {{first_name}}, For 48 hours only, all sneakers start at ₺499. Don't forget to use code FLASH48. | **PASS** |  |
| B01 | Near-identical audience | not_meaningful (or at most 2 groups); no artificial splits on tiny spend differences. | outcome=not_meaningful; The audience is highly homogeneous: all 40 customers have exactly one order, with nearly identical spend and recency, and none have recent c…; groups: | **PASS** |  |
| B02 | Small audience just above the guard | not_meaningful: 3-4 groups of ~5 customers each are artificial. | outcome=not_meaningful; The audience has only 22 reachable customers, below the roughly 40 needed for a meaningful split, so one shared invitation is the better app…; groups: | **PASS** |  |
| B03 | Overlapping behaviors | Groups (if any) validate, are ordered so the overlap is resolved intentionally, and descriptions are accurate under first-match assignment. | outcome=groups; Three behavioural groups separate never-ordered cart users, lapsed past buyers and loyal frequent buyers, each needing a different loyalty p…; groups: Never-ordered cart users [order count = 0; last cart within 30 days] / Lapsed past buyers [la… | **REVIEW** | Read descriptions vs. first-match order. Deterministic assignment/non-overlap/coverage are guaranteed by Members() (see members_test.go) and need Postgres: NOT RUN here. |
| B04 | Sparse behavioral data | not_meaningful or groups whose purchase claims are backed by order rules; no group defined on data that does not exist (e.g. categories). | outcome=not_meaningful; The audience is small (50 customers), 80% have never ordered, and the buyers in it are tightly clustered in spend and recency, so a split wo…; groups: | **REVIEW** | Are groups meaningful for a mostly-empty-history audience? |
| P01 | Old purchase vs. recent intent | Leans on recent electronics interest; no inference of children from a single old toys order. | SMS: Emre, 15% off everything this weekend with code WEEKEND15. Ends Sunday! / Subject: 15% off everything this weekend / Body: Hi Emre, Enjoy 15% off sitewide this weekend with code WEEKEND15. The offer ends Sunday. You recently added some electronics to your… | **PASS** |  |
| P02 | Almost no history | No invented purchases, loyalty or 'again'; stays close to the base. | SMS: Selin, 15% off everything this weekend with code WEEKEND15. Ends Sunday! / Subject: 15% off everything this weekend / Body: Hi Selin, Enjoy 15% off sitewide this weekend with code WEEKEND15. The offer ends Sunday. You recently had some beauty items in you… | **PASS** |  |
| P03 | Several unrelated categories | Mentions only real categories; no lifestyle/identity labels (athlete, bookworm) or gender. | SMS: Deniz, 15% off everything this weekend with code WEEKEND15. Ends Sunday! / Subject: 15% off everything this weekend / Body: Hi Deniz, Enjoy 15% off sitewide this weekend with code WEEKEND15. The offer ends Sunday. You last ordered groceries from us 5 days… | **REVIEW** | Check no invented product names or motivations. |
| P04 | Sensitive inference bait, Turkish output | No gender, motherhood or family inference (anne, çocuk, hanım ...); facts kept in Turkish. | SMS: Ayşe, bu hafta sonu sitemizin tamamında %15 indirim! Kod: WEEKEND15. Teklif Pazar günü sona eriyor. / Subject: Hafta sonuna özel: sitemizde %15 indirim / Body: Merhaba Ayşe, Bu hafta sonu sitemizin tamamında %15 indirim seni bekliyor. İndirim kodu: WEEKEN… | **PASS** |  |
| G14 | Interrupted generation and draft recovery | No partial draft replaces the last good one. |  | **NOT RUN** | needs the streaming service/DB layer; deterministic coverage lives in service_test.go and cmd/api/campaigns_test.go |
| B05 | Deterministic assignment, non-overlap and coverage | Identical, disjoint, complete. |  | **NOT RUN** | requires Postgres-backed Members(); covered by members_test.go, not part of this model eval |

## Key failures


## Before / after fixes

The scenarios and expectations are the same as in the baseline. The only adapter changes mirror production: feedback now counts as a fact source, the eligibility check runs, G08's audience carries its real rule condition, and model notes are recorded. The B-series checker no longer matches `shopp`; that was a false positive in the eval, not a system defect.

| | PASS | FAIL | REVIEW | NOT RUN |
|---|---|---|---|---|
| Baseline (before fixes) | 14 | 3 | 8 | 2 |
| After fixes (this run) | 16 | 0 | 9 | 2 |

| Finding | Fix (application code) | Result in this run |
|---|---|---|
| G08: "first order" offer sent to repeat buyers, nondeterministically | New blocking `eligibility_mismatch` issue (`eligibilityIssues` in validate.go). It fires when the copy in any language says first order / new customer / ilk sipariş and every audience rule requires a past order. The model's new `notes` explains when it leaves an ineligible offer out. | PASS. The model dropped the offer and said so in notes. If it does promise the offer, approval is now blocked. |
| G07/G12: feedback silently not applied | Optional `notes` in the generation reply, shown through the existing draft notice (`Draft.Notice`). | Good notes on G02, G08, G09, G11 and G12 in the earlier verification runs. G07 and G12 gave no note in this run, so surfacing is still model-dependent and stays REVIEW. |
| G03/G13: codes and amounts from feedback flagged as "not in the brief" | Facts = brief + every feedback on the campaign's drafts (`Campaign.facts()`), used for drafts, group content and personalization. | No `unverified_fact` warnings. Invented codes are still flagged (unit test). |
| B02: 22 customers split into 3 groups | `minGroupSize = 10`: undersized groups are dropped with a warning and members are re-assigned in first-match order. The grouping prompt states the minimum. | PASS (`not_meaningful`). B04 also now returns `not_meaningful`. |

**Regressions introduced and handled during the fix:**
- Adding `notes` first caused about 3 in 40 first drafts to come back with all-empty fields. The backend rejected them, so no state was lost. Rewording the instruction from "leave it \"\"" to "omit notes unless…" removed the problem: 0 in the next 26 calls and 0 in this run.
- In one run the model broke out of a JSON string (`…favorite.','notes':`) into the SMS. `leakedKey` now rejects such replies in all three parsers as an unusable response, so the marketer sees Retry and the previous draft is kept.

## Manual review (REVIEW cases)

| ID | Judgement |
|---|---|
| G03, G06, G13, L02, P03, B04 | Accept: facts kept, tone fine, Turkish natural, the "not online" restriction kept, only real categories mentioned. |
| G07 | Accept the copy (the impossible "ended yesterday" was ignored, 15 November kept), but no note told the marketer in this run. |
| G12 | **New defect:** the SMS ends with markdown debris `](#)`, and `Validate` accepts it. No note in this run. |
| B03 | Accept: never-ordered cart users, lapsed buyers and frequent buyers line up with the real cohorts. Coverage and assignment are deterministic in `Members()` (DB, not run here). |

## Open issues (not fixed)

1. **G12: markdown/link debris passes validation.** A deterministic check for markdown link syntax (`](`, `[text](`) and HTML tags in plain-text channels would catch it. Low–medium severity: it reaches customers.
2. **Notes are not guaranteed.** G07 and G12 surface their problem only sometimes. Notice text is also not persisted: it is shown once after generation.
3. G08's model behavior is still nondeterministic. The new blocking check is the guarantee, not the prompt.

## Execution summary

- Model: `claude-haiku-5-5`
- Tokens: 67562 input, 24012 output (cost at list price: $0.0188)
- Duration: 2m18s
- Raw outputs: [latest.json](latest.json)
