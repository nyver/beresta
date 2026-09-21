import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Task 10.4: every CSS `animation` declared outside a
 * `prefers-reduced-motion` guard must have a matching
 * `@media (prefers-reduced-motion: reduce)` override that turns it off, so a
 * future animated feature cannot ship without a reduced-motion path. jsdom
 * cannot evaluate `prefers-reduced-motion` itself, so this checks the
 * stylesheet's text directly rather than computed style.
 */

const styles = readFileSync(path.join(__dirname, "styles.css"), "utf8");

function selectorsWithAnimationDeclarations(css: string): string[] {
  const withoutReducedMotionBlocks = css.replace(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\n\}\n/g, "");
  const matches = [...withoutReducedMotionBlocks.matchAll(/([^{}\n]+)\{[^{}]*\banimation:\s*(?!none\b)[^{}]*\}/g)];
  return matches.map((match) => match[1].trim());
}

describe("reduced motion", () => {
  it("declares a prefers-reduced-motion override block", () => {
    expect(styles).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  });

  it("every animated selector has a matching reduced-motion override", () => {
    const animated = selectorsWithAnimationDeclarations(styles);
    expect(animated.length).toBeGreaterThan(0);
    for (const selector of animated) {
      expect(styles, `expected a prefers-reduced-motion override disabling "${selector}"`).toMatch(
        new RegExp(`prefers-reduced-motion:\\s*reduce\\)\\s*\\{[^}]*${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{[^}]*animation:\\s*none`),
      );
    }
  });
});
