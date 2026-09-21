import { describe, expect, it } from "vitest";

/**
 * WCAG 2.1 relative luminance/contrast ratio (task 8.4's "WCAG AA
 * contrast"). This duplicates the color literals from styles.css rather
 * than reading the stylesheet, since there is no shared token source yet -
 * task 10.1/10.2 introduces the semantic manifest and an enforcement check
 * that supersedes this hand-maintained list. Until then, this guards the
 * specific text/background pairs already audited against WCAG AA (4.5:1
 * for normal text) so a future color edit cannot silently regress them.
 */
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
  ["#5a5148", "#fff", "default body text on white"],
  ["#5a5148", "#faf8f4", "default body text on the shell's off-white panels"],
  ["#5a5148", "#eee2d0", "default body text on the hover/highlight tint"],
  ["#3a332a", "#fff", "primary/heading text on white"],
  ["#1e1b18", "#fff", "high-emphasis text on white"],
  // #6e6659: the muted/secondary color used for note previews, dates, the
  // key-protection hint, save-status line, and small icon-only buttons -
  // darkened from the original #8a7f6f (3.93:1 on white), which failed
  // AA for this normal-size text.
  ["#6e6659", "#fff", "muted/secondary text on white"],
  ["#6e6659", "#faf8f4", "muted/secondary text on the shell's off-white panels"],
  ["#6b4f2a", "#fff", "accent text/links on white"],
  ["#8a2c1f", "#fff", "destructive text on white"],
];

describe("color contrast (WCAG AA)", () => {
  it.each(TEXT_ON_BACKGROUND_PAIRS)("%s on %s (%s) meets 4.5:1", (fg, bg) => {
    expect(contrastRatio(fg, bg)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL_TEXT);
  });
});
