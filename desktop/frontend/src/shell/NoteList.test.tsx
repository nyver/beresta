import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { fakeNote, mockLocaleCatalog, mockSettings } from "../testUtils";
import { formatNoteTimestamp } from "../format";
import { NoteList, type NoteListMeta, type NoteListProps } from "./NoteList";

const emptyMeta = new Map<string, NoteListMeta>();

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
      <NoteList notes={notes} loading={false} selectedNoteId="" onSelect={onSelect} noteMetaById={emptyMeta} />
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
        <NoteList notes={[]} loading onSelect={vi.fn()} selectedNoteId="" noteMetaById={emptyMeta} />
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
        <StatefulNoteList notes={notes} loading={false} noteMetaById={emptyMeta} />
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

  it("highlights matching terms in a note's title", async () => {
    mockLocaleCatalog();
    mockSettings();
    render(
      <I18nProvider>
        <NoteList
          notes={[fakeNote({ title: "Grocery list for Sunday" })]}
          loading={false}
          selectedNoteId=""
          onSelect={vi.fn()}
          noteMetaById={emptyMeta}
          highlightTerms={["grocery", "sunday"]}
        />
      </I18nProvider>,
    );

    const row = await screen.findByRole("option");
    expect(row).toHaveTextContent("Grocery list for Sunday");
    const marks = row.querySelectorAll("mark");
    expect(Array.from(marks).map((mark) => mark.textContent)).toEqual(["Grocery", "Sunday"]);
  });

  it("shows a custom empty message when provided", async () => {
    mockLocaleCatalog();
    mockSettings();
    render(
      <I18nProvider>
        <NoteList
          notes={[]}
          loading={false}
          selectedNoteId=""
          onSelect={vi.fn()}
          noteMetaById={emptyMeta}
          emptyMessage="search.no_results"
        />
      </I18nProvider>,
    );

    expect(await screen.findByText("search.no_results")).toBeInTheDocument();
    expect(screen.queryByText("shell.notelist_empty")).not.toBeInTheDocument();
  });

  // task 11.2: proves the note list actually stays virtualized (not just
  // visually, but in DOM node count) at the account's documented 20,000-note
  // ceiling, and that keyboard selection keeps computing the right index
  // even when the currently selected note is far outside whatever window
  // the virtualizer happens to have rendered - the case a small fixed-size
  // fixture (every other test above) cannot exercise, since a handler bug
  // like "assume the selected row is in the rendered set" would pass there
  // but silently misbehave at scale.
  it("keeps the DOM bounded and keyboard selection correct on a virtualized 20,000-note list", async () => {
    mockLocaleCatalog();
    mockSettings();
    const notes = Array.from({ length: 20000 }, (_, index) => fakeNote({ title: `Note ${index}` }));
    const midIndex = 10000;
    const onSelect = vi.fn();
    const { rerender } = render(
      <I18nProvider>
        <NoteList
          notes={notes}
          loading={false}
          selectedNoteId={notes[midIndex].id}
          onSelect={onSelect}
          noteMetaById={emptyMeta}
        />
      </I18nProvider>,
    );

    // Virtualization's entire point is bounding rendered DOM nodes
    // regardless of collection size; this is the one assertion that would
    // actually catch a regression to rendering every row.
    const listbox = await screen.findByRole("listbox");
    expect(screen.getAllByRole("option").length).toBeLessThan(100);

    listbox.focus();
    expect(listbox).toHaveFocus();

    // The selected note (index 10000) is far outside the rendered window
    // (near index 0), so this proves handleKeyDown's `notes.findIndex`
    // lookup - not some assumption that the selected row is mounted -
    // drives the next index.
    fireEvent.keyDown(listbox, { key: "ArrowDown" });
    expect(onSelect).toHaveBeenLastCalledWith(notes[midIndex + 1].id);
    expect(listbox).toHaveFocus();

    rerender(
      <I18nProvider>
        <NoteList
          notes={notes}
          loading={false}
          selectedNoteId={notes[midIndex + 1].id}
          onSelect={onSelect}
          noteMetaById={emptyMeta}
        />
      </I18nProvider>,
    );
    fireEvent.keyDown(listbox, { key: "ArrowUp" });
    expect(onSelect).toHaveBeenLastCalledWith(notes[midIndex].id);

    // Boundary clamping must still hold at scale: one past the last index
    // stays at the last index, never overflows past notes.length - 1.
    rerender(
      <I18nProvider>
        <NoteList
          notes={notes}
          loading={false}
          selectedNoteId={notes[notes.length - 1].id}
          onSelect={onSelect}
          noteMetaById={emptyMeta}
        />
      </I18nProvider>,
    );
    fireEvent.keyDown(listbox, { key: "ArrowDown" });
    expect(onSelect).toHaveBeenLastCalledWith(notes[notes.length - 1].id);
  });

  it("shows a preview snippet and formatted date when metadata is available", async () => {
    mockLocaleCatalog();
    mockSettings();
    const note = fakeNote({ title: "Grocery list" });
    const updatedMs = Date.UTC(2026, 0, 15);
    const meta = new Map<string, NoteListMeta>([[note.id, { updatedMs, preview: "milk, eggs, bread" }]]);
    render(
      <I18nProvider>
        <NoteList notes={[note]} loading={false} selectedNoteId="" onSelect={vi.fn()} noteMetaById={meta} />
      </I18nProvider>,
    );

    expect(await screen.findByText("milk, eggs, bread")).toBeInTheDocument();
    // formatNoteTimestamp itself is locale-aware (Intl), so this compares
    // against its own output rather than a hardcoded "Jan 15, 2026" that
    // would only match in an en-US test environment.
    expect(screen.getByText(formatNoteTimestamp(updatedMs))).toBeInTheDocument();
  });
});
