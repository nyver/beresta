import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeAccountInfo, fakeNote, fakeNotebook, fakeTag, mockLocaleCatalog, mockSettings } from "../testUtils";
import { Shell } from "./Shell";

function renderShell() {
  mockLocaleCatalog();
  mockSettings();
  const onLocked = vi.fn();
  render(
    <I18nProvider>
      <Shell account={fakeAccountInfo()} onLocked={onLocked} />
    </I18nProvider>,
  );
  return { onLocked };
}

describe("Shell", () => {
  it("loads and shows every note under All Notes by default", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Grocery list" })]);
    renderShell();

    expect(await screen.findByText("Grocery list")).toBeInTheDocument();
  });

  it("excludes tombstoned notes from every view", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([
      fakeNote({ title: "Live note" }),
      fakeNote({ title: "Deleted note", deleted: true }),
    ]);
    renderShell();

    expect(await screen.findByText("Live note")).toBeInTheDocument();
    expect(screen.queryByText("Deleted note")).not.toBeInTheDocument();
  });

  it("filters the note list to a selected notebook", async () => {
    const notebook = fakeNotebook({ name: "Work" });
    appMock.ListNotebooks.mockResolvedValue([notebook]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([
      fakeNote({ title: "In workspace root" }),
      fakeNote({ title: "In Work", notebook_id: notebook.id }),
    ]);
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("In workspace root");
    await user.click(await screen.findByRole("button", { name: "Work" }));

    expect(await screen.findByText("In Work")).toBeInTheDocument();
    expect(screen.queryByText("In workspace root")).not.toBeInTheDocument();
  });

  it("filters the note list to a selected tag via SearchByTag", async () => {
    const tag = fakeTag({ name: "urgent" });
    const taggedNote = fakeNote({ title: "Tagged note" });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([tag]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Other note" }), taggedNote]);
    appMock.SearchByTag.mockResolvedValue([{ note: taggedNote, rank: 0 }]);
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("Other note");
    await user.click(await screen.findByRole("button", { name: "urgent" }));

    expect(await screen.findByText("Tagged note")).toBeInTheDocument();
    expect(screen.queryByText("Other note")).not.toBeInTheDocument();
    expect(appMock.SearchByTag).toHaveBeenCalledWith(tag.id);
  });

  it("shows a retryable error when loading fails", async () => {
    appMock.ListNotebooks.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    renderShell();

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");

    appMock.ListNotebooks.mockResolvedValue([]);
    await userEvent.setup().click(screen.getByRole("button", { name: "common.retry" }));

    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("locks the account and reports it locked", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    appMock.LockAccount.mockResolvedValue(undefined);
    const { onLocked } = renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.lock_button" }));

    await waitFor(() => expect(onLocked).toHaveBeenCalled());
    expect(appMock.LockAccount).toHaveBeenCalled();
  });
});
