import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("renders the offline-first product identity", () => {
    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain("Beresta");
    expect(markup).toContain("Encrypted notes, available offline.");
  });
});
