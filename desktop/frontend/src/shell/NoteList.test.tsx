import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { fakeNote, mockLocaleCatalog, mockSettings } from "../testUtils";
import { NoteList, type NoteListProps } from "./NoteList";

/** StatefulNoteList mirrors how Shell actually drives NoteList (selection
 * flows back in as a prop), so an ArrowDown/ArrowDown sequence can be
 * asserted to move through the list rather than repeat the same index. */
function StatefulNoteList(props: Omit<NoteListProps, "selectedNoteId" | "onSelect">) {
  const [selectedNoteId, setSelectedNoteId] = useState("");
  return <NoteList {...props} selectedNoteId={selectedNoteId} onSelect={setSelectedNoteId} />;
}

function renderList(notes = [fakeNote({ title: "First" }), fakeNote({ title: "Second" })]) {
  mockLocaleCatalog();
  mockSettings();
  const onSelect = vi.fn();
  render(
    <I18nProvider>
      <NoteList notes={notes} loading={false} selectedNoteId="" onSelect={onSelect} />
    </I18nProvider>,
  );
  return { notes, onSelect };
}

describe("NoteList", () => {
  it("shows the empty state when there are no notes", async () => {
    renderList([]);
    expect(await screen.findByText("shell.notelist_empty")).toBeInTheDocument();
  });

  it("shows a loading state instead of the list", async () => {
    mockLocaleCatalog();
    mockSettings();
    render(
      <I18nProvider>
        <NoteList notes={[]} loading onSelect={vi.fn()} selectedNoteId="" />
      </I18nProvider>,
    );
    expect(await screen.findByText("common.loading")).toBeInTheDocument();
  });

  it("renders visible note rows and reports a click", async () => {
    const { notes, onSelect } = renderList();
    const user = userEvent.setup();

    const first = await screen.findByText("First");
    await user.click(first);

    expect(onSelect).toHaveBeenCalledWith(notes[0].id);
  });

  it("moves selection forward and backward with ArrowDown/ArrowUp", async () => {
    mockLocaleCatalog();
    mockSettings();
    const notes = [fakeNote({ title: "First" }), fakeNote({ title: "Second" }), fakeNote({ title: "Third" })];
    render(
      <I18nProvider>
        <StatefulNoteList notes={notes} loading={false} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const listbox = await screen.findByRole("listbox");
    listbox.focus();

    await user.keyboard("{ArrowDown}");
    expect(screen.getByText("First").closest("button")).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{ArrowDown}");
    expect(screen.getByText("Second").closest("button")).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{ArrowUp}");
    expect(screen.getByText("First").closest("button")).toHaveAttribute("aria-selected", "true");
  });
});
