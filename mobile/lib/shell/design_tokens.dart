/// Beresta's Flutter mapping of design/tokens.json (task 10.3's "map the
/// manifest to Flutter themes and primitives"). Values are transcribed here
/// rather than generated, and checked against the manifest by
/// test/design_tokens_test.dart - the same "duplicate, then mechanically
/// verify" approach desktop's tokens.css/tokens.manifest.test.ts uses.
///
/// Scope: this file covers the tokens that need an *exact* cross-platform
/// value (semantic status colors, spacing/radius/duration/icon-size
/// primitives used by hand-built widgets). General chrome (buttons, app bar,
/// form fields) keeps Flutter's own Material 3 `ColorScheme.fromSeed`
/// theming - seeded from `color.accent.default` so the derived palette
/// starts from the same brand color as desktop - rather than pinning every
/// Material role to a literal hex, since Material's algorithmic tonal
/// palette is the platform-native convention this app otherwise follows
/// (design.md: "retaining native interaction conventions").
///
/// `typography.fontFamily.base` (Inter) has no bundled Flutter equivalent
/// today. Rather than add a new font asset dependency for this pass, the
/// app keeps Material's platform default (Roboto) - a deliberate, recorded
/// exception, not an oversight.
///
/// Rem-to-logical-pixel conversion uses the same 16px base the web/desktop
/// stack assumes, so a manifest step means the same absolute size on both
/// clients: `px = rem * 16`.
library;

import "package:flutter/material.dart";

/// `spacing.*` (task 10.1), in logical pixels.
class AppSpacing {
  const AppSpacing._();

  static const double s0 = 0;
  static const double s1 = 2.4;
  static const double s2 = 3.2;
  static const double s3 = 4;
  static const double s4 = 5.6;
  static const double s5 = 8;
  static const double s6 = 9.6;
  static const double s7 = 12;
  static const double s8 = 16;
  static const double s9 = 20;
  static const double s10 = 24;
  static const double s11 = 32;
  static const double s12 = 48;
}

/// `radius.*`, in logical pixels. `full` has no fixed constant: a fully
/// circular element should use `BoxShape.circle`/`CircleBorder()` sized by
/// its own width/height, not a large literal radius.
class AppRadius {
  const AppRadius._();

  static const double none = 0;
  static const double xs = 2.4;
  static const double sm = 5.6;
  static const double md = 8;
  static const double pill = 16;
}

/// `border.width.*`, in logical pixels (already unit-equal to CSS `px`, no
/// rem conversion needed).
class AppBorderWidth {
  const AppBorderWidth._();

  static const double hairline = 1;
  static const double thick = 2;
}

/// `iconSize.*`, in logical pixels - matches `Icon.size`.
class AppIconSize {
  const AppIconSize._();

  static const double sm = 16;
  static const double md = 20;
  static const double lg = 24;
  static const double xl = 32;
}

/// `touchTarget.minimum` (44px/2.75rem, WCAG 2.5.8 AA). Flutter's own
/// `kMinInteractiveDimension` (48px) already exceeds this for standard
/// Material widgets; this constant exists for hand-built controls that
/// don't go through a Material widget's default hit-test area.
const double kTouchTargetMinimum = 44;

/// `animationDuration.*`.
class AppDuration {
  const AppDuration._();

  static const Duration instant = Duration.zero;
  static const Duration fast = Duration(milliseconds: 120);
  static const Duration base = Duration(milliseconds: 200);
  static const Duration slow = Duration(milliseconds: 800);
}

/// `color.status.*`, `color.accent.*`, and `color.inverse.*` - the semantic
/// colors that must render identically to desktop wherever this app
/// represents the same sync/backup/diff state or lock-screen privacy
/// surface, independent of Material's seeded tonal palette.
class AppColors {
  const AppColors._();

  static const Color accentDefault = Color(0xFF6B4F2A);
  static const Color accentHoverTint = Color(0xFFEEE2D0);
  static const Color accentHighlight = Color(0xFFE8C887);

  static const Color statusInfo = Color(0xFF2F6F9F);
  static const Color statusSuccess = Color(0xFF3A6B2A);
  static const Color statusWarning = Color(0xFF8A5A1F);
  static const Color statusWarningStrong = Color(0xFFB36A20);
  static const Color statusDanger = Color(0xFF8A2C1F);
  static const Color statusDangerStrong = Color(0xFFB23B3B);
  static const Color statusNeutral = Color(0xFF7D7469);

  static const Color inverseBackground = Color(0xFF2B2118);
  static const Color inverseText = Color(0xFFF8F6F1);
}
