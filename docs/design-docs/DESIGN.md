# Campaign Studio design specification

Status: agreed visual direction; proposed UX specification for implementation.
Scope: marketer brief → audience alternatives → human selection; later content, approval and delivery.
Functional conflicts must be resolved against the actual repository requirements, not inferred from screenshots.

## 1. Visual direction

Floating Conservatory: a pale celadon canvas, floating forest navigation, matte white panels and pistachio selection. Botanical character comes primarily from color, soft depth and spacing. Avoid large leaf illustrations behind work content.

| Token | Value / use |
| --- | --- |
| canvas | `#EEF3EB` |
| surface | `#FFFFFF` |
| surface-muted | `#F7F9F4` |
| forest | `#204D3C`: navigation, main text, primary dark actions |
| selected | `#DDE8B5`: dark-text selection and light CTA |
| selected-surface | `#F1F6E1` |
| text-muted | `#536D60` |
| border | `#DCE4D9` |
| danger | `#A33932`, pale background `#FFF1EE` |
| success | `#287D60`, always accompanied by text/icon |
| font | Montserrat 400 / 500 / 600; no synthetic bold |
| type | page 28/36, section 20/28, panel 16/24, body 14/22, helper 12/18 px |
| spacing | 4, 8, 12, 16, 24, 32, 48 px |
| radii | control 10, panel 16–20, badge fully rounded |
| shadow | `0 10px 28px rgb(32 77 60 / 0.08)` |

Use existing semantic theme variables; adapt rather than create a second theme system. Put values in one existing or new theme stylesheet. No repeated inline hex values across components. Font may be self-hosted WOFF2 or use the existing font pipeline; retain its license if distributing files.

### Material and motion

- Readable, opaque data surfaces. Mild glass is limited to top bar and sticky action bar, with an opaque fallback.
- Brand accents are sparse. Recommended badge is neutral; user selection owns the green highlight.
- Floating sidebar: 16px canvas gutter. Desktop width around 200–220px. Content max width around 1440px.
- Subtle botanical shadow only in outer whitespace; no decorative leaves in tables, chat or text fields.
- Hover 150ms; focus/selection 180–220ms; panel enter 200–250ms, opacity plus at most 8px translation.
- Selection can rise 2px with a restrained shadow. No ambient loops, fake progress percentages or animated counts on live delivery updates.
- Honor `prefers-reduced-motion`; retain visible focus and text labels independent of motion or color.
- Verify contrast in the actual component states. Light pistachio requires dark text.

## 2. Shared shell and navigation

Main routes: Campaigns and Audience Explorer only. No dead notification/profile/settings controls.
Campaign workspace: campaign name + save state, stage navigation (Audience / Content / Review / Delivery), optional assistant panel, working surface, one primary footer action.
Only expose stages implemented in the current milestone. Do not present disabled future features as working.

Agent and chat are the same contextual assistant, not separate product pages. Desktop assistant width ~30%, working surface ~70%. Both can scroll independently with an accessible input and footer that never cover content.

## 3. View inventory (eight surfaces, not eight routes)

| ID | Surface | Required content and next action |
| --- | --- | --- |
| V1 | Campaign list | Search, status filter, name/status/channels/audience/updated; New campaign / Resume. Empty state has one action. |
| V2 | Brief | Spacious multiline input, concise instructions, 3 optional examples; Generate audiences. Example clicks populate rather than auto-submit. |
| V3 | Assistant workspace | User messages, concise assistant replies, current activity and persistent composer. Results live in the work surface. |
| V4 | Audience proposals | 2–3 alternatives, short rationale, scope difference, rules, total and per-channel reach, recommendation, explicit selection. |
| V5 | Audience detail / Explorer | Conditions, count, reachable channels, paginated members and purchase summaries. Reuse as proposal drawer and Explorer content. |
| V6 | Channel content | Email/SMS controls, editable subject/body, recipient preview, draft indicators. Preserve per-channel edits. |
| V7 | Review + confirm | Audience, eligible channel counts, exact final copy, Edit links, explicit send confirmation. |
| V8 | Delivery detail | Queued/sent/failed counts per channel, status table, last update and error details. Retry only if supported safely by backend. |

Current wireframes: V2, V3+V4, V6, V7. V5 is a drawer/detail variant. V1 and V8 use straightforward list/status patterns; no extra wireframes needed.

## 4. Core interactions

### Audience proposals

Show aligned horizontal options on wide screens. Name + concise rationale; total people and channel reach; readable targeting rules; a concrete “What changes” description. An expanded rule section may be used for long conditions. Raw JSON belongs only in optional technical details.

- Recommend an option but **do not preselect** it. Wireframe 02 shows the state **after** user selection.
- Selecting is separate from continuing. One footer CTA: Use this audience.
- Distinguish recommendation, selected, loading, invalid and empty states.
- Count comes from backend queries. Do not show invented counts, lift predictions or confidence scores.
- Email and SMS overlap. Never sum them into a unique audience total.
- Preview members opens a drawer; closing restores scroll and selection.
- If regeneration changes conditions or counts, clear selection and ask the user to choose again.
- Zero audience or zero reach on all selected channels blocks continuation with a useful refinement action.

### Assistant

Enter sends, Shift+Enter adds a line; account for IME composition. Disable duplicate submission while pending. Do not promise cancellation unless supported.
Ask only for blocking clarification. Present a short targeting explanation, not hidden reasoning or internal prompts.
Show real activity such as preparing alternatives or checking counts; do not fabricate tool activity.
Keep the brief/history on failure. Inline Retry repeats only the failed operation.
Auto-scroll only when already near the bottom. Otherwise show a New response control.
Follow-up requests revise the current brief using conversation context. If conversational revision is not in the backend contract, support editing the brief and regenerating; do not simulate conversation locally as a working agent.

### Content and review (later)

User edits are preserved per channel. Agent revisions must not silently replace edited text: preview/apply the revision.
Preview substituted personalization with clearly labeled sample recipient data. Flag unsupported/unresolved variables.
Changing audience preserves content but marks it for review. Any audience, channel or content change invalidates prior approval.
Send requires a concrete review and confirmation, not a chat utterance. Sending disables repeated confirmation. Backend owns durable send state and duplicate prevention.

## 5. Flows

| Entry / condition | Sequence and recovery |
| --- | --- |
| New campaign | List → brief → generation → compare → select → content → review → confirm → delivery. |
| From Explorer | Inspect segment → Use in campaign → selected audience workspace, if supported. |
| Refine | Follow-up or edit brief → regenerate → recompute → clear old selection → choose. |
| Preview | Proposal → audience drawer → close → same proposal position and selection. |
| API/validation failure | Preserve input and last valid result, mark it as previous, show specific failure + retry. Never silently substitute fake data. |
| Partially valid alternatives | Show valid alternatives and a concise warning; do not make invalid ones selectable. |
| No match / unreachable | Explain zero result or channel reach → refine conditions or channel. No automatic broadening. |
| Review edit | Return to relevant stage → retain other work → regenerate affected counts → review again. |
| Send | Confirm → server job accepted → delivery; refresh reads server state and never starts a second send. |
| Partial delivery failure | Show per-recipient errors. If retry exists, target only eligible failed items and preserve successful sends. |
| Navigate / reload | Persist draft if supported; otherwise warn before discarding unsaved work. Never label unsaved data “Saved”. |

## 6. Shared component states

| Component | States |
| --- | --- |
| Brief / chat composer | empty, populated, pending, validation failure, API failure |
| Proposal group | loading, ready, partial failure, all invalid, empty |
| Proposal | recommended, selected, unselected, invalid, zero match, expanded |
| Audience drawer | loading, populated, zero members, failed; server pagination/search |
| Message editor | generating, generated, edited, invalid, sample preview |
| Send action | blocked, ready, confirming, submitting, accepted, failed |
| Delivery | queued, running, completed, partial failure, failed, connection stale |

Use inline actionable errors. Toasts may acknowledge transient success; critical failures remain visible. Accessible live region announces completion/errors without stealing focus. Dialogs/drawers trap focus and restore it to the trigger; Escape closes safe dismissible overlays.

## 7. Responsive and accessibility

Desktop: sidebar + assistant + work surface. Tablet: collapse sidebar, assistant can be toggled. Mobile: Assistant / Preview switch preserving state; proposals stack; review is single-column; table scroll is contained.
Footer stays above mobile safe area and does not hide the final item. Inputs use 16px on mobile, target sizes about 44px, keyboard-visible focus. Do not require hover for essential information. No drag-only interactions.
