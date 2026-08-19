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
  ListNotebooks: vi.fn(),
  ListTags: vi.fn(),
  ListNotes: vi.fn(),
  SearchByTag: vi.fn(),
  GetNoteDocument: vi.fn(),
  CommitNoteBody: vi.fn(),
};

(globalThis as unknown as { go: { main: { App: typeof appMock } } }).go = {
  main: { App: appMock },
};

// jsdom has no layout engine, so every element reports a 0x0 box and has
// no ResizeObserver at all. @tanstack/react-virtual (the NoteList's
// virtualization, see shell/NoteList.tsx) measures its scroll container
// through @tanstack/virtual-core's getRect(), which reads
// offsetWidth/offsetHeight specifically (not getBoundingClientRect and
// not clientHeight/clientWidth) - without stubbing those two, the
// virtualizer always computes a 0-height viewport and renders zero rows.
// ResizeObserver itself is stubbed separately only so the library's
// unconditional `new ResizeObserver(...)` call does not throw; it never
// needs to actually fire for a static test scenario like this one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver =
  ResizeObserverStub;
Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, value: 800 });
Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, value: 600 });

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
