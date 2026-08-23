import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeSavedSearch, fakeTag, mockLocaleCatalog, mockSavedSearches, mockSettings } from "../testUtils";
import { SearchBar, type SearchBarHandle } from "./SearchBar";
import { main } from "../../wailsjs/go/models";

function fakeNoteDTO(title: string): main.NoteDTO {
  return main.NoteDTO.createFrom({
    id: `note-${title}`,
    workspace_id: "workspace",
    notebook_id: "",
    title,
    pinned: false,
    archived: false,
    deleted: false,
    created_unix_ms: 0,
  });
}

function fakeResult(title: string): main.SearchResultDTO {
  return main.SearchResultDTO.createFrom({ note: fakeNoteDTO(title), rank: 0 });
}

function renderSearchBar(tags: main.TagDTO[] = [], notes: main.NoteDTO[] = []) {
  mockLocaleCatalog();
  mockSettings();
  mockSavedSearches();
  const ref = createRef<SearchBarHandle>();
  const onResultsChange = vi.fn();
  render(
    <I18nProvider>
      <SearchBar ref={ref} tags={tags} notes={notes} onResultsChange={onResultsChange} />
    </I18nProvider>,
  );
  return { ref, onResultsChange };
}

/** Opens the collapsed "Filters & saved searches" disclosure, which hides
 * the tag/date/saved-search controls by default (task 1.1). */
async function openAdvanced(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /search.advanced_toggle/ }));
}

describe("SearchBar", () => {
  it("reports no active search and never calls Search while every field is empty", async () => {
    const { onResultsChange } = renderSearchBar();

    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith(null, []));
    expect(appMock.Search).not.toHaveBeenCalled();
  });

  it("matches notes by partial title, entirely client-side", async () => {
    const notes = [fakeNoteDTO("Grocery list"), fakeNoteDTO("Other note")];
    const { onResultsChange } = renderSearchBar([], notes);
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "rocery");

    await waitFor(() =>
      expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Grocery list")], ["rocery"]),
    );
    expect(appMock.Search).not.toHaveBeenCalled();
  });

  it("excludes deleted notes from the client-side title match", async () => {
    const notes = [main.NoteDTO.createFrom({ ...fakeNoteDTO("Grocery list"), deleted: true })];
    const { onResultsChange } = renderSearchBar([], notes);
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "grocery");

    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith([], ["grocery"]));
  });

  it("routes text containing a filter token through the backend instead of a literal title match", async () => {
    appMock.Search.mockResolvedValue([fakeResult("Urgent item")]);
    const { onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "tag:urgent");

    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("tag:urgent"));
    await waitFor(() =>
      expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Urgent item")], ["tag:urgent"]),
    );
  });

  it("ignores a stale search response that resolves after a newer one", async () => {
    let resolveStale: (value: main.SearchResultDTO[]) => void = () => {};
    let resolveFresh: (value: main.SearchResultDTO[]) => void = () => {};
    appMock.Search.mockImplementationOnce(() => new Promise((resolve) => (resolveStale = resolve)));
    appMock.Search.mockImplementationOnce(() => new Promise((resolve) => (resolveFresh = resolve)));
    const { onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    // A filter token forces the backend path (see the test above) so the
    // debounce/race-guard behavior being tested here is actually exercised.
    await user.type(screen.getByPlaceholderText("search.placeholder"), "deleted:true");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("deleted:true"));
    await user.type(screen.getByPlaceholderText("search.placeholder"), " x");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("deleted:true x"));

    // Resolve out of order: the newer query answers first, the stale one
    // answers after it - the stale response must not overwrite the newer
    // result.
    resolveFresh([fakeResult("Fresh result")]);
    await waitFor(() =>
      expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Fresh result")], ["deleted:true", "x"]),
    );
    resolveStale([fakeResult("Stale result")]);

    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(onResultsChange).not.toHaveBeenCalledWith([fakeResult("Stale result")], ["deleted:true"]);
  });

  it("composes the tag filter into the query text", async () => {
    const tag = fakeTag({ name: "urgent" });
    appMock.Search.mockResolvedValue([]);
    renderSearchBar([tag]);
    const user = userEvent.setup();

    await openAdvanced(user);
    await user.selectOptions(screen.getByText("search.tag_filter_all").closest("select")!, tag.id);

    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("tag:urgent"));
  });

  it("composes after/before date filters into the query text", async () => {
    appMock.Search.mockResolvedValue([]);
    renderSearchBar();
    const user = userEvent.setup();

    await openAdvanced(user);
    const [afterInput, beforeInput] = document.querySelectorAll<HTMLInputElement>('input[type="date"]');
    await user.type(afterInput, "2026-01-01");
    await user.type(beforeInput, "2026-01-02");

    await waitFor(() => {
      const last = appMock.Search.mock.calls.at(-1)?.[0] as string | undefined;
      expect(last).toMatch(/^after:\d+ before:\d+$/);
    });
    const query = appMock.Search.mock.calls.at(-1)![0] as string;
    const [, afterMS, beforeMS] = query.match(/^after:(\d+) before:(\d+)$/)!;
    expect(Number(afterMS)).toBe(new Date("2026-01-01T00:00:00").getTime());
    expect(Number(beforeMS)).toBe(new Date("2026-01-02T23:59:59.999").getTime());
  });

  it("saves the current query as a new saved search and reloads the list", async () => {
    appMock.CreateSavedSearch.mockResolvedValue(fakeSavedSearch({ id: "saved-1", name: "My search" }));
    const { onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    await openAdvanced(user);
    await user.type(screen.getByPlaceholderText("search.placeholder"), "project");
    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith([], ["project"]));
    await user.type(screen.getByPlaceholderText("search.save_new_placeholder"), "My search");
    appMock.ListSavedSearches.mockResolvedValue([fakeSavedSearch({ id: "saved-1", name: "My search" })]);
    await user.click(screen.getByRole("button", { name: "search.save_button" }));

    await waitFor(() => expect(appMock.CreateSavedSearch).toHaveBeenCalledWith("My search", "project"));
    await waitFor(() => expect(appMock.ListSavedSearches).toHaveBeenCalledTimes(2));
  });

  it("loads a saved search's query into the text box and switches to Update", async () => {
    const saved = fakeSavedSearch({ id: "saved-1", name: "Urgent items", query: "tag:urgent" });
    mockLocaleCatalog();
    mockSettings();
    appMock.ListSavedSearches.mockResolvedValue([saved]);
    appMock.Search.mockResolvedValue([]);
    const onResultsChange = vi.fn();
    render(
      <I18nProvider>
        <SearchBar tags={[]} notes={[]} onResultsChange={onResultsChange} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await openAdvanced(user);
    await screen.findByText("Urgent items");
    await user.selectOptions(screen.getByLabelText("search.saved_searches_label"), "saved-1");

    expect(screen.getByPlaceholderText("search.placeholder")).toHaveValue("tag:urgent");
    expect(screen.getByRole("button", { name: "search.update_button" })).toBeInTheDocument();
    // The loaded query still carries its tag: token, so it still resolves
    // through the backend rather than being read as a literal title match.
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("tag:urgent"));
  });

  it("deletes the selected saved search and clears the selection", async () => {
    const saved = fakeSavedSearch({ id: "saved-1", name: "Urgent items", query: "tag:urgent" });
    mockLocaleCatalog();
    mockSettings();
    appMock.ListSavedSearches.mockResolvedValueOnce([saved]).mockResolvedValue([]);
    appMock.DeleteSavedSearch.mockResolvedValue(undefined);
    appMock.Search.mockResolvedValue([]);
    render(
      <I18nProvider>
        <SearchBar tags={[]} notes={[]} onResultsChange={vi.fn()} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await openAdvanced(user);
    await screen.findByText("Urgent items");
    await user.selectOptions(screen.getByLabelText("search.saved_searches_label"), "saved-1");
    await user.click(await screen.findByRole("button", { name: "search.delete_button" }));

    await waitFor(() => expect(appMock.DeleteSavedSearch).toHaveBeenCalledWith("saved-1"));
    expect(screen.queryByRole("button", { name: "search.delete_button" })).not.toBeInTheDocument();
  });

  it("clear() resets every field through the imperative handle", async () => {
    const { ref, onResultsChange } = renderSearchBar([], [fakeNoteDTO("Note")]);
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "note");
    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Note")], ["note"]));

    act(() => {
      ref.current?.clear();
    });

    expect(screen.getByPlaceholderText("search.placeholder")).toHaveValue("");
    await waitFor(() => expect(onResultsChange).toHaveBeenLastCalledWith(null, []));
  });
});
