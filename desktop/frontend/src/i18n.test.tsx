import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type ApiError } from "./api";
import { I18nProvider, useI18n } from "./i18n";
import { appMock } from "./setupTests";
import { mockSettings } from "./testUtils";

/**
 * TestErrorMessageNeverLeaksRawBackendText covers task 4.3's seeded-secret
 * guarantee for the "errors" surface: errorMessage is the single chokepoint
 * every screen uses instead of rendering ApiError.message directly (see
 * api.ts's doc comment on ApiError - Message is an English-only backend
 * diagnostic fallback, never shown to the user on its own). It must
 * resolve every code - known or entirely unrecognized - to catalog text,
 * never to the raw message a backend error happened to carry, even when
 * that message contains a seeded secret.
 */
describe("errorMessage", () => {
  it("never returns the raw backend message, for a known or an unrecognized error code", async () => {
    mockSettings();
    appMock.Catalog.mockResolvedValue({
      locale: "en",
      strings: {
        "errors.locked": "The account is locked.",
        "errors.internal": "Something went wrong. Please try again.",
      },
      supported: ["en", "ru"],
    });

    const { result } = renderHook(() => useI18n(), {
      wrapper: ({ children }) => <I18nProvider>{children}</I18nProvider>,
    });
    await waitFor(() => expect(result.current.ready).toBe(true));

    const seed = "seeded-secret-backend-detail-canary";
    const cases: ApiError[] = [
      { code: "locked", message: `db open failed: ${seed}` },
      { code: "an_entirely_unrecognized_code", message: `sqlite: near ")": ${seed}` },
    ];

    for (const error of cases) {
      const rendered = result.current.errorMessage(error);
      expect(rendered).not.toContain(seed);
      expect(rendered.length).toBeGreaterThan(0);
    }
    expect(result.current.errorMessage(cases[0])).toBe("The account is locked.");
    expect(result.current.errorMessage(cases[1])).toBe(
      "Something went wrong. Please try again.",
    );
  });
});
