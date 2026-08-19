import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

/**
 * appMock stubs the generated Wails bindings (window.go.main.App.*) that
 * every screen calls through src/api.ts. Wails only installs window.go at
 * runtime inside the real desktop shell, so component tests need this
 * stand-in; tests configure individual methods per case with
 * mockResolvedValueOnce/mockRejectedValueOnce.
 */
export const appMock = {
  Status: vi.fn(),
  GetSettings: vi.fn(),
  UpdateSettings: vi.fn(),
  Catalog: vi.fn(),
  DefaultDatabasePath: vi.fn(),
  CreateAccount: vi.fn(),
  UnlockAccount: vi.fn(),
  LockAccount: vi.fn(),
  PickSaveDestination: vi.fn(),
};

(globalThis as unknown as { go: { main: { App: typeof appMock } } }).go = {
  main: { App: appMock },
};

beforeEach(() => {
  for (const fn of Object.values(appMock)) {
    fn.mockReset();
  }
});

// @testing-library/react's own automatic-cleanup registration only fires
// when it detects a global `afterEach` (i.e. Vitest's `test.globals: true`
// config), which this project deliberately does not enable so every test
// file keeps its explicit `import { ... } from "vitest"` style. Without
// this, unmounted trees from a previous test stay in the jsdom document
// and later tests' role/label queries can match stale duplicates.
afterEach(() => {
  cleanup();
});
