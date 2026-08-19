import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeSavedSearch, fakeTag, mockLocaleCatalog, mockSavedSearches, mockSettings } from "../testUtils";
import { SearchBar, type SearchBarHandle } from "./SearchBar";
import { main } from "../../wailsjs/go/models";

function fakeResult(title: string): main.SearchResultDTO {
  return main.SearchResultDTO.createFrom({
    note: {
      id: `note-${title}`,
      workspace_id: "workspace",
      notebook_id: "",
      title,
      pinned: false,
      archived: false,
      deleted: false,
      created_unix_ms: 0,
    },
    rank: 0,
  });
}

function renderSearchBar(tags: main.TagDTO[] = []) {
  mockLocaleCatalog();
  mockSettings();
  mockSavedSearches();
  const ref = createRef<SearchBarHandle>();
  const onResultsChange = vi.fn();
  render(
    <I18nProvider>
      <SearchBar ref={ref} tags={tags} onResultsChange={onResultsChange} />
    </I18nProvider>,
  );
  return { ref, onResultsChange };
}

describe("SearchBar", () => {
  it("reports no active search and never calls Search while every field is empty", async () => {
    const { onResultsChange } = renderSearchBar();

    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith(null, []));
    expect(appMock.Search).not.toHaveBeenCalled();
  });

  it("debounces typed text and reports the results plus highlight terms", async () => {
    appMock.Search.mockResolvedValue([fakeResult("Grocery list")]);
    const { onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "grocery list");

    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("grocery list"));
    await waitFor(() =>
      expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Grocery list")], ["grocery", "list"]),
    );
  });

  it("ignores a stale search response that resolves after a newer one", async () => {
    let resolveStale: (value: main.SearchResultDTO[]) => void = () => {};
    let resolveFresh: (value: main.SearchResultDTO[]) => void = () => {};
    appMock.Search.mockImplementationOnce(() => new Promise((resolve) => (resolveStale = resolve)));
    appMock.Search.mockImplementationOnce(() => new Promise((resolve) => (resolveFresh = resolve)));
    const { onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "al");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("al"));
    await user.type(screen.getByPlaceholderText("search.placeholder"), "p");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("alp"));

    // Resolve out of order: the newer query ("alp") answers first, the
    // stale one ("al") answers after it - the stale response must not
    // overwrite the newer result.
    resolveFresh([fakeResult("Alp result")]);
    await waitFor(() => expect(onResultsChange).toHaveBeenCalledWith([fakeResult("Alp result")], ["alp"]));
    resolveStale([fakeResult("Al result")]);

    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(onResultsChange).not.toHaveBeenCalledWith([fakeResult("Al result")], ["al"]);
  });

  it("composes the tag filter into the query text", async () => {
    const tag = fakeTag({ name: "urgent" });
    appMock.Search.mockResolvedValue([]);
    renderSearchBar([tag]);
    const user = userEvent.setup();

    await user.selectOptions(screen.getByText("search.tag_filter_all").closest("select")!, tag.id);

    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("tag:urgent"));
  });

  it("composes after/before date filters into the query text", async () => {
    appMock.Search.mockResolvedValue([]);
    renderSearchBar();
    const user = userEvent.setup();

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
    appMock.Search.mockResolvedValue([]);
    appMock.CreateSavedSearch.mockResolvedValue(fakeSavedSearch({ id: "saved-1", name: "My search" }));
    renderSearchBar();
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "project");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("project"));
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
        <SearchBar tags={[]} onResultsChange={onResultsChange} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await screen.findByText("Urgent items");
    await user.selectOptions(screen.getByLabelText("search.saved_searches_label"), "saved-1");

    expect(screen.getByPlaceholderText("search.placeholder")).toHaveValue("tag:urgent");
    expect(screen.getByRole("button", { name: "search.update_button" })).toBeInTheDocument();
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
        <SearchBar tags={[]} onResultsChange={vi.fn()} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await screen.findByText("Urgent items");
    await user.selectOptions(screen.getByLabelText("search.saved_searches_label"), "saved-1");
    await user.click(await screen.findByRole("button", { name: "search.delete_button" }));

    await waitFor(() => expect(appMock.DeleteSavedSearch).toHaveBeenCalledWith("saved-1"));
    expect(screen.queryByRole("button", { name: "search.delete_button" })).not.toBeInTheDocument();
  });

  it("clear() resets every field through the imperative handle", async () => {
    appMock.Search.mockResolvedValue([fakeResult("Note")]);
    const { ref, onResultsChange } = renderSearchBar();
    const user = userEvent.setup();

    await user.type(screen.getByPlaceholderText("search.placeholder"), "note");
    await waitFor(() => expect(appMock.Search).toHaveBeenCalledWith("note"));

    act(() => {
      ref.current?.clear();
    });

    expect(screen.getByPlaceholderText("search.placeholder")).toHaveValue("");
    await waitFor(() => expect(onResultsChange).toHaveBeenLastCalledWith(null, []));
  });
});
