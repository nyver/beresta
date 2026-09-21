import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Task 10.2's "hard-coded feature colors fail a lint or repository check"
 * (design.md decision 6). tokens.css is the only file allowed to declare a
 * literal color; every feature stylesheet must reference a `var(--color-*)`
 * custom property instead, so a future edit cannot reintroduce a one-off hex
 * value that silently drifts from design/tokens.json.
 */

const HEX_OR_RGBA_COLOR = /#[0-9a-fA-F]{3,8}\b|rgba?\(/g;

const FEATURE_STYLESHEETS = ["styles.css"];

describe("feature stylesheets contain no hard-coded colors", () => {
  it.each(FEATURE_STYLESHEETS)("%s uses only token custom properties for color", (fileName) => {
    const contents = readFileSync(path.join(__dirname, fileName), "utf8");
    const matches = contents.match(HEX_OR_RGBA_COLOR) ?? [];
    expect(matches, `found hard-coded color literal(s) in ${fileName}: ${matches.join(", ")}`).toHaveLength(0);
  });
});
