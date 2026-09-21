import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Mechanically verifies tokens.css against design/tokens.json (task 10.1's
 * manifest) so the two cannot silently drift apart - the "map the manifest
 * to CSS custom properties" half of task 10.2. A category-name alias table
 * covers the two spots where the CSS variable prefix intentionally differs
 * from the JSON key ("spacing" -> "space", "animationDuration" ->
 * "duration"); "typography"'s children are promoted to top-level CSS
 * prefixes (`font-size-*`, `font-weight-*`, ...) instead of nesting under a
 * "typography-" prefix, matching tokens.css. "motion" is a runtime rule
 * (task 10.4), not a static value, and is excluded.
 */

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const manifest = JSON.parse(readFileSync(path.join(repoRoot, "design", "tokens.json"), "utf8")) as Record<
  string,
  unknown
>;
const tokensCss = readFileSync(path.join(__dirname, "tokens.css"), "utf8");

const CATEGORY_ALIASES: Record<string, string> = {
  spacing: "space",
  animationDuration: "duration",
};

const EXCLUDED_CATEGORIES = new Set(["motion", "version"]);

function kebabCase(segment: string): string {
  return segment.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase();
}

function cssVarNameForPath(pathSegments: string[]): string {
  return pathSegments.map(kebabCase).join("-");
}

interface FlatToken {
  cssVar: string;
  value: unknown;
}

function flatten(node: unknown, prefixSegments: string[], out: FlatToken[]): void {
  if (node === null || typeof node !== "object") {
    out.push({ cssVar: cssVarNameForPath(prefixSegments), value: node });
    return;
  }
  for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
    if (key.startsWith("$")) continue;
    flatten(value, [...prefixSegments, key], out);
  }
}

function resolveExpected(rawValue: unknown): string {
  if (typeof rawValue !== "string") return String(rawValue);
  const refMatch = rawValue.match(/^\{([\w.]+)\}$/);
  if (!refMatch) return rawValue;
  const [refCategory, ...rest] = refMatch[1].split(".");
  const prefix = CATEGORY_ALIASES[refCategory] ?? refCategory;
  return `var(--${cssVarNameForPath([prefix, ...rest])})`;
}

function readCssVar(name: string): string | undefined {
  const match = tokensCss.match(new RegExp(`--${name}:\\s*([^;]+);`));
  return match?.[1]?.trim();
}

const tokens: FlatToken[] = [];
for (const [category, value] of Object.entries(manifest)) {
  if (category.startsWith("$") || EXCLUDED_CATEGORIES.has(category)) continue;
  if (category === "typography") {
    for (const [group, groupValue] of Object.entries(value as Record<string, unknown>)) {
      if (group.startsWith("$")) continue;
      flatten(groupValue, [group], tokens);
    }
    continue;
  }
  const prefix = CATEGORY_ALIASES[category] ?? category;
  flatten(value, [prefix], tokens);
}

describe("tokens.css matches design/tokens.json", () => {
  it("found a non-trivial number of tokens to check", () => {
    expect(tokens.length).toBeGreaterThan(40);
  });

  it.each(tokens.map((t) => [t.cssVar, t.value] as const))("--%s matches the manifest", (cssVar, rawValue) => {
    const expected = resolveExpected(rawValue);
    const actual = readCssVar(cssVar);
    expect(actual, `--${cssVar} is missing from tokens.css`).toBeDefined();
    expect(actual).toBe(expected);
  });
});
