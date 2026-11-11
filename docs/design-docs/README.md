# Campaign Studio — Design handoff

Selected direction: **04 Floating Conservatory**, with **Montserrat**.

This is a small repository-ready case-study handoff, not a standalone design-system project.
Copy `design-docs/` into the repository root. No install or build is required to read it.

## Start here

1. Read the repository's requirements first; they govern functionality.
2. Read [DESIGN.md](DESIGN.md) for the chosen visual system and UX decisions.
3. Open [wireframes/index.html](wireframes/index.html) in a browser, or inspect the PNGs below.
4. Read [IMPLEMENTATION.md](IMPLEMENTATION.md) before making changes.

| Frame | Purpose | Milestone |
| --- | --- | --- |
| [01 Brief](wireframes/01-brief.png) | First-use campaign input | Current |
| [02 Proposals](wireframes/02-proposals.png) | Assistant + audience comparison | Current |
| [03 Content](wireframes/03-content.png) | Editable channel content + preview | Later |
| [04 Review](wireframes/04-review.png) | Final summary + confirmation state | Later |

Each frame has an editable, Figma-importable SVG beside its PNG. SVGs are static vector layouts, not native Figma components or auto-layout. Install Montserrat locally for accurate SVG typography. PNG wireframes use an available fallback font; **production must use Montserrat**.

[Visual reference](reference/floating-conservatory.png) defines material, palette and mood. Wireframes define layout and interaction intent. **DESIGN.md wins where details conflict.** The reference's presentation heading, decorative leaves and sample counts are not production requirements.

## Files

- `DESIGN.md`: tokens, eight-view inventory, flows, component states and responsive rules.
- `IMPLEMENTATION.md`: limited first milestone, dependencies and agent handoff.
- `wireframes/`: four SVGs, four PNGs and an offline gallery.
- `reference/`: selected art direction.

No application code, fonts, dependencies or backend contracts are bundled. All example data is illustrative. Generated copy is not evidence of conversion performance.
