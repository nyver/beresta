import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * WCAG 2.1 relative luminance/contrast ratio (task 8.4's "WCAG AA
 * contrast"). Reads its color values from design/tokens.json (task 10.1's
 * manifest) instead of duplicating hand-copied literals, so a future
 * palette edit in the manifest cannot silently drift out of sync with the
 * pairs already audited against WCAG AA (4.5:1 for normal text) below.
 */

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const manifest = JSON.parse(readFileSync(path.join(repoRoot, "design", "tokens.json"), "utf8")) as {
  color: {
    text: Record<string, string>;
    background: Record<string, string>;
    accent: Record<string, string>;
    status: Record<string, string>;
  };
};
const { text, background, accent, status } = manifest.color;
function relativeLuminance(hex: string): number {
  const short = hex.replace("#", "");
  const value = short.length === 3 ? short.split("").map((digit) => digit + digit).join("") : short;
  const [r, g, b] = [0, 2, 4].map((offset) => parseInt(value.slice(offset, offset + 2), 16));
  const [rs, gs, bs] = [r, g, b].map((channel) => {
    const normalized = channel / 255;
    return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
}

function contrastRatio(hexA: string, hexB: string): number {
  const [lighter, darker] = [relativeLuminance(hexA), relativeLuminance(hexB)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
}

// Primary body-text colors against the backgrounds they actually render on
// in styles.css (main content areas, cards, and the muted sidebar tint).
const WCAG_AA_NORMAL_TEXT = 4.5;
const TEXT_ON_BACKGROUND_PAIRS: Array<[fg: string, bg: string, context: string]> = [
  [text.secondary, background.surface, "default body text on white"],
  [text.secondary, background.surfaceSunken, "default body text on the shell's off-white panels"],
  [text.secondary, accent.hoverTint, "default body text on the hover/highlight tint"],
  [text.heading, background.surface, "primary/heading text on white"],
  [text.primary, background.surface, "high-emphasis text on white"],
  // text.muted: the muted/secondary color used for note previews, dates,
  // the key-protection hint, save-status line, and small icon-only buttons -
  // darkened from the original #8a7f6f (3.93:1 on white), which failed AA
  // for this normal-size text.
  [text.muted, background.surface, "muted/secondary text on white"],
  [text.muted, background.surfaceSunken, "muted/secondary text on the shell's off-white panels"],
  [accent.default, background.surface, "accent text/links on white"],
  [status.danger, background.surface, "destructive text on white"],
];

describe("color contrast (WCAG AA)", () => {
  it.each(TEXT_ON_BACKGROUND_PAIRS)("%s on %s (%s) meets 4.5:1", (fg, bg) => {
    expect(contrastRatio(fg, bg)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL_TEXT);
  });
});
