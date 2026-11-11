# Implementation handoff

## Read order

Actual project requirements → existing source and API contracts → DESIGN.md → reference image and four wireframes.
The pack does not establish an API contract or override case requirements. Inspect before editing.

## First milestone

Implement the existing brief → LLM audience alternatives → backend validation/counts → human selection flow, with the Floating Conservatory theme.

- Implement brief, proposals and member preview using real endpoints.
- Put an assistant beside proposals if backend conversation support exists. Otherwise offer brief editing/regeneration without a fabricated multi-turn agent.
- Keep recommendation distinct from explicit selection.
- End with selected audience state/callback using existing conventions. Do not build later campaign features just because their wireframes are included.
- Handle pending, empty, invalid and API error states. Preserve user input.
- Reuse the existing Audience Explorer and customer data components.

Later: campaign persistence/list if absent, channel content generation/editing, review/approval, delivery jobs/status. Follow actual case requirements for providers and retry semantics. No scheduling, team administration, template marketplace or rich-text builder unless explicitly required.

## Dependencies

Use existing equivalents first; do not replace a working library for visual consistency alone.

| Need | Choice |
| --- | --- |
| Vue primitives | shadcn-vue, only when a suitable existing component system is absent |
| Styling | Existing Tailwind + semantic CSS theme variables |
| Icons | One Lucide family; retain installed Vue package/version |
| Typography | Montserrat 400/500/600 through existing font pipeline |
| Motion | CSS and Vue Transition initially; Motion for Vue only for justified layout/spring interactions |
| Tables | Standard table + server pagination/search; no table engine unless already used or needed |
| Chat | Small Vue components using existing API/SSE transport; no new agent/chat framework |

Suggested product components: CampaignShell, BriefComposer, ChatPanel, AudienceProposal, ChannelReach, AudienceDrawer. Later: MessagePreview, ReviewSummary, DeliveryStatus. Reuse generic buttons/dialogs/forms rather than wrapping everything.

No Storybook, token generator, second UI framework, animation framework by default or custom font package is required.

## Verification and acceptance

- Readable selected/unselected/recommended states; keyboard selection and focus restoration work.
- API responses supply actual counts; SMS/email are not summed into unique people.
- Brief survives errors; repeat requests are disabled while pending; no hardcoded fake result fallback.
- Empty audience and unreachable selected channels have clear recovery.
- No dead navigation or save indicators that claim nonexistent persistence.
- Four frames guide composition; use responsive implementation rather than fixed 1440px coordinates.
- Run existing relevant type/build checks and focused tests for changed behavior. Inspect desktop and mobile, including error/loading states.
- Keep existing environment running. Do not restart/rebuild Docker services without an explicit request.

## Copyable agent task

Read the repository requirements, design-docs/DESIGN.md and this file. Inspect existing Vue UI, theme, endpoints and segmentation implementation. Implement only the First milestone above. Use reference/floating-conservatory.png for visual direction and wireframes/01-brief.svg plus 02-proposals.svg for composition; these paths are relative to design-docs/. Treat 03 and 04 as later context. Reuse working code and dependencies; do not fabricate API behavior. Run relevant checks, visually verify desktop/mobile states, and summarize changes and remaining limitations.

## Optional repository instruction

Add to an existing AGENTS.md or CLAUDE.md:

> For UI work, read design-docs/DESIGN.md and design-docs/IMPLEMENTATION.md first. The selected reference is design-docs/reference/floating-conservatory.png. Later wireframes do not expand the current implementation scope.
