import { Delta } from "quill";
import { describe, expect, it } from "vitest";

import { stripUnsupportedFormats } from "./pasteFormat";

// The node argument is unused by stripUnsupportedFormats (it only inspects
// the accumulated delta), so any placeholder satisfies the Quill Matcher
// signature in these tests.
const anyNode = document.createElement("div");

describe("stripUnsupportedFormats", () => {
  it("keeps every canonical format attribute unchanged", () => {
    const delta = new Delta()
      .insert("bold", { bold: true })
      .insert("italic", { italic: true })
      .insert("strike", { strike: true })
      .insert("code", { code: true })
      .insert("link", { link: "https://example.com" })
      .insert("\n", { header: 2 })
      .insert("\n", { list: "ordered" })
      .insert("\n", { list: "bullet" })
      .insert("\n", { blockquote: true })
      .insert("\n", { "code-block": true });

    expect(stripUnsupportedFormats(anyNode, delta).ops).toEqual(delta.ops);
  });

  it("drops non-canonical attributes but keeps the text", () => {
    const delta = new Delta().insert("underlined", { underline: true }).insert("colored", { color: "#ff0000" });

    const result = stripUnsupportedFormats(anyNode, delta);

    expect(result.ops).toEqual([{ insert: "underlined" }, { insert: "colored" }]);
  });

  it("keeps canonical attributes alongside dropped ones on the same run", () => {
    const delta = new Delta().insert("mixed", { bold: true, underline: true, font: "serif" });

    const result = stripUnsupportedFormats(anyNode, delta);

    expect(result.ops).toEqual([{ insert: "mixed", attributes: { bold: true } }]);
  });

  it("drops a heading level outside 1-3 instead of clamping it", () => {
    const delta = new Delta().insert("Heading four").insert("\n", { header: 4 });

    const result = stripUnsupportedFormats(anyNode, delta);

    expect(result.ops).toEqual([{ insert: "Heading four" }, { insert: "\n" }]);
  });

  it("keeps heading levels 1 through 3", () => {
    for (const level of [1, 2, 3]) {
      const delta = new Delta().insert("Heading").insert("\n", { header: level });
      const result = stripUnsupportedFormats(anyNode, delta);
      expect(result.ops).toEqual([{ insert: "Heading" }, { insert: "\n", attributes: { header: level } }]);
    }
  });

  it("drops a checklist list value instead of keeping an unrenderable checkbox", () => {
    // Quill's List format also accepts "checked"/"unchecked" for a
    // checklist item (a paste source like Notion or another Quill app can
    // produce this even though no toolbar button offers it) - the Go
    // core's canonical projection only understands "bullet"/"ordered".
    const delta = new Delta().insert("Buy milk").insert("\n", { list: "checked" });

    const result = stripUnsupportedFormats(anyNode, delta);

    expect(result.ops).toEqual([{ insert: "Buy milk" }, { insert: "\n" }]);
  });

  it("keeps bullet and ordered list values", () => {
    for (const value of ["bullet", "ordered"]) {
      const delta = new Delta().insert("Item").insert("\n", { list: value });
      const result = stripUnsupportedFormats(anyNode, delta);
      expect(result.ops).toEqual([{ insert: "Item" }, { insert: "\n", attributes: { list: value } }]);
    }
  });

  it("passes through ops with no attributes unchanged", () => {
    const delta = new Delta().insert("plain text");

    expect(stripUnsupportedFormats(anyNode, delta).ops).toEqual([{ insert: "plain text" }]);
  });
});
