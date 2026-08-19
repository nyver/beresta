import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { RevisionsPanel } from "./RevisionsPanel";
import { main } from "../../wailsjs/go/models";

function fakeRevision(overrides: Partial<main.RevisionDTO> = {}): main.RevisionDTO {
  return {
    id: "revision-1",
    checkpoint: false,
    created_unix_ms: Date.UTC(2026, 0, 1, 12, 0, 0),
    ...overrides,
  };
}

function renderPanel(noteId = "note-1") {
  mockLocaleCatalog();
  mockSettings();
  const onBeforeRestore = vi.fn().mockResolvedValue(undefined);
  const onRestored = vi.fn();
  render(
    <I18nProvider>
      <RevisionsPanel noteId={noteId} onBeforeRestore={onBeforeRestore} onRestored={onRestored} />
    </I18nProvider>,
  );
  return { onBeforeRestore, onRestored };
}

describe("RevisionsPanel", () => {
  it("shows the empty state when a note has no revisions", async () => {
    appMock.ListRevisions.mockResolvedValue([]);
    renderPanel();

    expect(await screen.findByText("revisions.empty")).toBeInTheDocument();
  });

  it("lists revisions newest first with a checkpoint badge", async () => {
    appMock.ListRevisions.mockResolvedValue([
      fakeRevision({ id: "rev-old", created_unix_ms: Date.UTC(2026, 0, 1) }),
      fakeRevision({ id: "rev-new", created_unix_ms: Date.UTC(2026, 0, 2), checkpoint: true }),
    ]);
    renderPanel();

    const rows = await screen.findAllByRole("button", { name: /2026/ });
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("revisions.checkpoint_badge");
    expect(rows[1]).not.toHaveTextContent("revisions.checkpoint_badge");
  });

  it("shows a localized error when the revision list fails to load", async () => {
    appMock.ListRevisions.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderPanel();

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("diffs the selected revision against its predecessor", async () => {
    appMock.ListRevisions.mockResolvedValue([
      fakeRevision({ id: "rev-1", created_unix_ms: Date.UTC(2026, 0, 1) }),
      fakeRevision({ id: "rev-2", created_unix_ms: Date.UTC(2026, 0, 2) }),
    ]);
    appMock.DiffRevisions.mockResolvedValue([
      { op: "equal", text: "kept" },
      { op: "insert", text: "added" },
    ]);
    renderPanel("note-1");
    const user = userEvent.setup();

    const rows = await screen.findAllByRole("button", { name: /2026/ });
    await user.click(rows[0]); // newest first, so rows[0] is rev-2

    await waitFor(() => expect(appMock.DiffRevisions).toHaveBeenCalledWith("note-1", "rev-1", "rev-2"));
    expect(await screen.findByText("added")).toBeInTheDocument();
    expect(screen.getByText("kept")).toBeInTheDocument();
  });

  it("diffs the oldest revision against empty content", async () => {
    appMock.ListRevisions.mockResolvedValue([fakeRevision({ id: "rev-1" })]);
    appMock.DiffRevisions.mockResolvedValue([{ op: "insert", text: "first line" }]);
    renderPanel("note-1");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: /2026/ }));

    await waitFor(() => expect(appMock.DiffRevisions).toHaveBeenCalledWith("note-1", "", "rev-1"));
  });

  it("restores the selected revision after a two-step confirmation, and reports it restored", async () => {
    appMock.ListRevisions.mockResolvedValue([fakeRevision({ id: "rev-1" })]);
    appMock.DiffRevisions.mockResolvedValue([]);
    appMock.RestoreRevision.mockResolvedValue(undefined);
    const { onBeforeRestore, onRestored } = renderPanel("note-1");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: /2026/ }));
    await user.click(await screen.findByRole("button", { name: "revisions.restore_button" }));
    expect(screen.getByText("revisions.restore_confirm")).toBeInTheDocument();

    appMock.ListRevisions.mockResolvedValue([fakeRevision({ id: "rev-1" })]);
    await user.click(screen.getByRole("button", { name: "revisions.restore_confirm_button" }));

    await waitFor(() => expect(appMock.RestoreRevision).toHaveBeenCalledWith("note-1", "rev-1"));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
  });

  it("flushes pending editor edits via onBeforeRestore before calling RestoreRevision", async () => {
    appMock.ListRevisions.mockResolvedValue([fakeRevision({ id: "rev-1" })]);
    appMock.DiffRevisions.mockResolvedValue([]);
    appMock.RestoreRevision.mockResolvedValue(undefined);
    const callOrder: string[] = [];
    const onBeforeRestore = vi.fn().mockImplementation(async () => {
      callOrder.push("flush");
    });
    appMock.RestoreRevision.mockImplementation(async () => {
      callOrder.push("restore");
    });
    mockLocaleCatalog();
    mockSettings();
    render(
      <I18nProvider>
        <RevisionsPanel noteId="note-1" onBeforeRestore={onBeforeRestore} onRestored={vi.fn()} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: /2026/ }));
    await user.click(await screen.findByRole("button", { name: "revisions.restore_button" }));
    await user.click(screen.getByRole("button", { name: "revisions.restore_confirm_button" }));

    await waitFor(() => expect(appMock.RestoreRevision).toHaveBeenCalled());
    expect(onBeforeRestore).toHaveBeenCalled();
    expect(callOrder).toEqual(["flush", "restore"]);
  });

  it("cancels the restore confirmation without calling RestoreRevision", async () => {
    appMock.ListRevisions.mockResolvedValue([fakeRevision({ id: "rev-1" })]);
    appMock.DiffRevisions.mockResolvedValue([]);
    renderPanel("note-1");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: /2026/ }));
    await user.click(await screen.findByRole("button", { name: "revisions.restore_button" }));
    await user.click(screen.getByRole("button", { name: "common.cancel" }));

    expect(screen.queryByText("revisions.restore_confirm")).not.toBeInTheDocument();
    expect(appMock.RestoreRevision).not.toHaveBeenCalled();
  });
});
