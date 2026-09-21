import "dart:convert";
import "dart:io";

import "package:beresta/shell/design_tokens.dart";
import "package:flutter/material.dart";
import "package:flutter_test/flutter_test.dart";

/// Mechanically verifies lib/shell/design_tokens.dart against
/// design/tokens.json (task 10.1's manifest) - the Flutter half of task
/// 10.3's "add parity checks against the manifest", mirroring how desktop's
/// tokens.manifest.test.ts checks tokens.css. Length tokens use the same
/// 16px-per-rem conversion design_tokens.dart documents.
void main() {
  late Map<String, dynamic> manifest;

  setUpAll(() {
    manifest = jsonDecode(_findDesignTokensFile().readAsStringSync()) as Map<String, dynamic>;
  });

  double remToPx(String rem) => double.parse(rem.replaceAll("rem", "")) * 16;
  double msToDuration(String ms) => double.parse(ms.replaceAll("ms", ""));

  group("AppSpacing matches design/tokens.json spacing.*", () {
    final cases = <String, double Function()>{
      "1": () => AppSpacing.s1,
      "2": () => AppSpacing.s2,
      "3": () => AppSpacing.s3,
      "4": () => AppSpacing.s4,
      "5": () => AppSpacing.s5,
      "6": () => AppSpacing.s6,
      "7": () => AppSpacing.s7,
      "8": () => AppSpacing.s8,
      "9": () => AppSpacing.s9,
      "10": () => AppSpacing.s10,
      "11": () => AppSpacing.s11,
      "12": () => AppSpacing.s12,
    };
    cases.forEach((key, actual) {
      test("spacing.$key", () {
        final manifestValue = (manifest["spacing"] as Map<String, dynamic>)[key] as String;
        expect(actual(), closeTo(remToPx(manifestValue), 0.001));
      });
    });
  });

  group("AppRadius matches design/tokens.json radius.*", () {
    final cases = <String, double Function()>{
      "none": () => AppRadius.none,
      "xs": () => AppRadius.xs,
      "sm": () => AppRadius.sm,
      "md": () => AppRadius.md,
      "pill": () => AppRadius.pill,
    };
    cases.forEach((key, actual) {
      test("radius.$key", () {
        final manifestValue = (manifest["radius"] as Map<String, dynamic>)[key] as String;
        expect(actual(), closeTo(remToPx(manifestValue), 0.001));
      });
    });
  });

  group("AppBorderWidth matches design/tokens.json border.width.*", () {
    test("hairline", () {
      final width = manifest["border"]["width"]["hairline"] as String;
      expect(AppBorderWidth.hairline, double.parse(width.replaceAll("px", "")));
    });

    test("thick", () {
      final width = manifest["border"]["width"]["thick"] as String;
      expect(AppBorderWidth.thick, double.parse(width.replaceAll("px", "")));
    });
  });

  group("AppIconSize matches design/tokens.json iconSize.*", () {
    final cases = <String, double Function()>{
      "sm": () => AppIconSize.sm,
      "md": () => AppIconSize.md,
      "lg": () => AppIconSize.lg,
      "xl": () => AppIconSize.xl,
    };
    cases.forEach((key, actual) {
      test("iconSize.$key", () {
        final manifestValue = (manifest["iconSize"] as Map<String, dynamic>)[key] as String;
        expect(actual(), closeTo(remToPx(manifestValue), 0.001));
      });
    });
  });

  test("kTouchTargetMinimum matches design/tokens.json touchTarget.minimum", () {
    final manifestValue = manifest["touchTarget"]["minimum"] as String;
    expect(kTouchTargetMinimum, closeTo(remToPx(manifestValue), 0.001));
  });

  group("AppDuration matches design/tokens.json animationDuration.*", () {
    final cases = <String, Duration Function()>{
      "instant": () => AppDuration.instant,
      "fast": () => AppDuration.fast,
      "base": () => AppDuration.base,
      "slow": () => AppDuration.slow,
    };
    cases.forEach((key, actual) {
      test("animationDuration.$key", () {
        final manifestValue = (manifest["animationDuration"] as Map<String, dynamic>)[key] as String;
        expect(actual().inMilliseconds.toDouble(), msToDuration(manifestValue));
      });
    });
  });

  group("AppColors matches design/tokens.json color.*", () {
    final cases = <String, Color Function()>{
      "accent.default": () => AppColors.accentDefault,
      "accent.hoverTint": () => AppColors.accentHoverTint,
      "accent.highlight": () => AppColors.accentHighlight,
      "status.info": () => AppColors.statusInfo,
      "status.success": () => AppColors.statusSuccess,
      "status.warning": () => AppColors.statusWarning,
      "status.warningStrong": () => AppColors.statusWarningStrong,
      "status.danger": () => AppColors.statusDanger,
      "status.dangerStrong": () => AppColors.statusDangerStrong,
      "status.neutral": () => AppColors.statusNeutral,
      "inverse.background": () => AppColors.inverseBackground,
      "inverse.text": () => AppColors.inverseText,
    };
    cases.forEach((path, actual) {
      test("color.$path", () {
        final segments = path.split(".");
        dynamic node = manifest["color"];
        for (final segment in segments) {
          node = node[segment];
        }
        final expectedHex = (node as String).replaceFirst("#", "").toUpperCase();
        final actualHex = actual().toARGB32().toRadixString(16).substring(2).toUpperCase();
        expect(actualHex, expectedHex);
      });
    });
  });
}

/// design/tokens.json lives at the repository root; `flutter test` runs
/// with the package directory (mobile/) as the working directory, so walk
/// upward until it is found rather than hard-coding a relative depth.
File _findDesignTokensFile() {
  var dir = Directory.current;
  for (var i = 0; i < 6; i++) {
    final candidate = File("${dir.path}/design/tokens.json");
    if (candidate.existsSync()) return candidate;
    final parent = dir.parent;
    if (parent.path == dir.path) break;
    dir = parent;
  }
  throw StateError("could not locate design/tokens.json above ${Directory.current.path}");
}
