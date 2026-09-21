# Semantic design token manifest

`tokens.json` in this directory is the single reviewed source of truth for the
values covered by specs/product-experience's "Shared visual and accessibility
language" requirement: spacing, typography, radius, elevation, border, focus,
icon size, animation duration, and semantic color roles. Windows
(`desktop/frontend`) and Android (`mobile`) each map this manifest to their
own platform-native layer (CSS custom properties and a Flutter
`ThemeExtension`, respectively - tasks 10.2 and 10.3); this document exists so
that mapping has one manifest to follow instead of two independently drifting
guesses.

## Review basis

Every value in `tokens.json` was harvested from the colors, spacing, radii,
and shadows already in production use in `desktop/frontend/src/styles.css`
and `mobile/lib/app.dart`, not invented fresh. Where two nearly-identical
values existed only because of organic drift (for example radius values of
`0.3rem`, `0.35rem`, and `0.4rem` used interchangeably across buttons, inputs,
and menu rows with no visible intent behind the difference), they were
consolidated into one token rather than preserved as separate roles. Task
10.2/10.3 migrations are expected to produce the resulting sub-pixel visual
nudges; none of them change a color's semantic meaning.

Consolidations made during review:

- **Radius**: `0.25rem`, `0.3rem`, `0.35rem`, and `0.4rem` (attachment
  thumbnails, buttons, inputs, icon buttons, list rows, menu items) collapse
  to `radius.sm` (`0.35rem`, the most common of the four).
- **Spacing**: `0.3rem` and `0.35rem` are both used as compact
  padding/gap on badges, pills, and small buttons with no consistent pattern
  choosing between them; they collapse to one spacing step (`4`, `0.35rem`).
- **Typography**: `0.7rem`/`0.75rem` collapse to `typography.fontSize.xs`
  (`0.75rem`, the larger and thus safer choice); `0.8rem`/`0.85rem`/`0.9rem`
  collapse to `typography.fontSize.sm` (`0.85rem`, the middle value).
- **Color**: the muted-text color already carries a documented history (see
  `colorContrast.test.ts`) - `#8a7f6f` was replaced by `#6e6659` during task
  8.4's WCAG AA pass. This manifest's `color.text.muted` continues from that
  audited value rather than reopening the contrast question.

## Category notes

- **spacing**: a numbered scale (`1` .. `12`) spanning `0.15rem`-`3rem`,
  matching the full range of gaps/padding/margins already used, including the
  `0.6rem` (`6`) step used consistently as compact-control horizontal padding
  (chips, small buttons). Numbered rather than t-shirt-sized so a future
  in-between value can be inserted without renaming every step around it. Not
  every step needs to appear on every screen; component authors pick the
  closest semantic fit rather than measuring pixels by hand.
- **typography**: `fontFamily.base` (Inter, falling back to Segoe UI) is a
  Windows/web font stack. Flutter has no bundled Inter today, so task 10.3
  must decide between bundling the font asset or mapping `fontFamily.base` to
  the platform default (Roboto) with a documented, deliberate exception - this
  manifest does not presume that answer.
- **radius**: `radius.full` (`9999px`) is for fully circular elements sized
  by their own width/height (for example `sync-status-dot`), not a literal
  border box.
- **elevation**: three levels only, matching the three real shadow depths in
  use today (popover/menu, snackbar, modal). A component that does not fit
  one of these three should not invent a fourth without updating this
  manifest first.
- **border**: only hairline (`1px`) and thick (`2px`) widths exist in the
  current UI (dividers/inputs vs. drag-over/focus emphasis); there is no
  `medium` step because nothing today uses one.
- **focus**: standardizes a visible focus treatment (`accent.default` ring,
  `2px` width, `2px` offset) for custom interactive elements that do not
  already get a correct native focus ring from the browser/OS. Task 8.4 found
  the existing hand-rolled `:focus`/`:focus-visible` overrides did not
  suppress the indicator, so this token formalizes what already worked by
  accident rather than changing behavior.
- **iconSize**: four steps from inline glyphs (`sm`) up to primary topbar
  icon buttons and thumbnails (`xl`).
- **touchTarget.minimum**: `2.75rem` (44px) is the WCAG 2.5.8 AA target size,
  recorded here as the goal several existing controls do not yet meet (for
  example the `1.25rem` notebook-tree disclosure toggle). This manifest
  records the target; closing the gap is explicitly task 10.5's scope, not
  this one's.
- **animationDuration** / **motion**: `slow` (`800ms`) matches the existing
  sync-status spinner exactly. `reducedMotionOverride` states the rule task
  10.4 implements: every duration collapses to `instant` under a
  reduced-motion preference, rather than each animated component inventing
  its own reduced-motion special case.
- **color**: grouped by role (`background`, `text`, `border`, `accent`,
  `status`, `highlight`, `overlay`, `inverse`) rather than by raw hue, so a
  future palette change only touches this manifest. `status.*` intentionally
  mirrors the four categories already used for sync/backup/error states
  (info, success, warning, danger) plus a neutral for "no signal yet"
  (offline/local-only) - it does not introduce a fifth category not already
  needed by an existing UI state.

## Dark mode is deliberately not introduced yet

design.md's "Build parallel native design-token layers from one semantic
manifest" decision states theme scope is all-or-nothing: "dark mode ships
only when every editor, dialog, menu, native bridge, and platform surface
passes the complete theme matrix." That matrix needs visual review on real
displays and the physical Windows/Android qualification passes (tasks
12.4/12.5) - neither is something this manifest or its consuming code can
self-certify. Task 10.4 therefore implements only the half of "reduced-motion
and complete-theme handling" that is independently verifiable today:
reduced-motion support (`tokens.css`'s `prefers-reduced-motion` override,
`design_tokens.dart`'s `AppDuration.resolve`). A second (dark) color set is
intentionally not added to this manifest; adding one is future work gated on
that qualification pass, not a gap in this review.

## What this manifest does not do

Defining and reviewing the manifest (this task, 10.1) does not migrate any
consuming code. `desktop/frontend/src/styles.css` and `mobile/lib/app.dart`
still hard-code the equivalent literal values today; tasks 10.2 and 10.3 wire
each platform's primitives to these tokens and add the enforcement checks
(`colorContrast.test.ts`'s own comment already anticipates this) that keep
feature code from reintroducing hard-coded values afterward.
