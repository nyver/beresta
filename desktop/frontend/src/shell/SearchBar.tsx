import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import {
  createSavedSearch,
  deleteSavedSearch,
  listSavedSearches,
  search,
  unwrapError,
  updateSavedSearch,
} from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface SearchBarHandle {
  /** Resets every field and reports back to "no active search". Shell.tsx
   * calls this when the user picks a notebook or tag from the sidebar, so
   * a stale search does not keep overriding that choice. */
  clear: () => void;
}

export interface SearchBarProps {
  tags: main.TagDTO[];
  /** Called whenever the active result set changes: null means no search
   * is active (fall back to the sidebar's notebook/tag browsing), an array
   * is the current result set (possibly empty), and highlightTerms are the
   * free-text words the note list should highlight. */
  onResultsChange: (results: main.SearchResultDTO[] | null, highlightTerms: string[]) => void;
}

const SEARCH_DEBOUNCE_MS = 150;

/**
 * composeQuery builds one string in the search-box filter language (see
 * store.ParseSearchQueryText) from the bare text box plus the tag/date/
 * deleted filter controls, so they all resolve through the same parser and
 * SearchNotes call the box alone would use. A tag name containing
 * whitespace cannot round-trip through this whitespace-tokenized language
 * (an existing backend constraint, not something this UI can work around);
 * the dropdown still selects it correctly; only composing it into free
 * text would break.
 */
function composeQuery(
  text: string,
  tagName: string,
  afterMS: number | null,
  beforeMS: number | null,
  includeDeleted: boolean,
): string {
  const parts: string[] = [];
  const trimmed = text.trim();
  if (trimmed) parts.push(trimmed);
  if (tagName) parts.push(`tag:${tagName}`);
  if (afterMS !== null) parts.push(`after:${afterMS}`);
  if (beforeMS !== null) parts.push(`before:${beforeMS}`);
  if (includeDeleted) parts.push("deleted:true");
  return parts.join(" ");
}

function startOfDayMS(value: string): number | null {
  if (!value) return null;
  const ms = new Date(`${value}T00:00:00`).getTime();
  return Number.isNaN(ms) ? null : ms;
}

function endOfDayMS(value: string): number | null {
  if (!value) return null;
  const ms = new Date(`${value}T23:59:59.999`).getTime();
  return Number.isNaN(ms) ? null : ms;
}

/**
 * SearchBar is the desktop note list's search box: instant (debounced)
 * full-text search composed with tag/date/deleted filter controls, plus
 * saved-query management (list, load, save-as-new, update, delete). It
 * calls the same App.Search / store.SearchNotes path measured by
 * core/store's 20,000-note / 150 ms benchmark (search_bench_test.go), so
 * that budget covers this UI's queries too.
 */
export const SearchBar = forwardRef<SearchBarHandle, SearchBarProps>(function SearchBar(
  { tags, onResultsChange },
  ref,
) {
  const { t, errorMessage } = useI18n();
  const [text, setText] = useState("");
  const [tagFilterId, setTagFilterId] = useState("");
  const [afterDate, setAfterDate] = useState("");
  const [beforeDate, setBeforeDate] = useState("");
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [savedSearches, setSavedSearches] = useState<main.SavedSearchDTO[]>([]);
  const [selectedSavedSearchId, setSelectedSavedSearchId] = useState("");
  const [saveName, setSaveName] = useState("");
  const [error, setError] = useState<string | null>(null);

  // onResultsChange is a fresh closure on every Shell render; depending on
  // it directly in the debounce effect below would re-run (and re-fetch)
  // on every unrelated Shell re-render. Routing it through a ref (updated
  // every render, but not itself a dependency) avoids that, the same
  // pattern AttachmentPanel uses for its onAttachFiles callback.
  const onResultsChangeRef = useRef(onResultsChange);
  useEffect(() => {
    onResultsChangeRef.current = onResultsChange;
  });

  // Guards against an in-flight search resolving out of order: the
  // debounce timer's own cleanup only cancels a still-pending setTimeout,
  // not a search() call already dispatched, so a slow response for an
  // earlier query can otherwise resolve after (and overwrite) a faster
  // response for a newer one.
  const latestRequestIdRef = useRef(0);

  const refreshSavedSearches = useCallback(() => {
    listSavedSearches()
      .then(setSavedSearches)
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))));
  }, [errorMessage]);

  useEffect(() => {
    refreshSavedSearches();
    // Loaded once on mount: saved searches only change through this
    // panel's own create/update/delete handlers, each of which already
    // re-fetches explicitly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const tagName = tags.find((tag) => tag.id === tagFilterId)?.name ?? "";
  const afterMS = startOfDayMS(afterDate);
  const beforeMS = endOfDayMS(beforeDate);
  const activeQuery = composeQuery(text, tagName, afterMS, beforeMS, includeDeleted);
  const hasQuery = activeQuery !== "";

  useEffect(() => {
    if (!hasQuery) {
      latestRequestIdRef.current += 1;
      setError(null);
      onResultsChangeRef.current(null, []);
      return;
    }
    const terms = text.trim() ? text.trim().split(/\s+/) : [];
    const timer = window.setTimeout(() => {
      const requestId = (latestRequestIdRef.current += 1);
      search(activeQuery)
        .then((results) => {
          if (latestRequestIdRef.current !== requestId) return;
          setError(null);
          onResultsChangeRef.current(results, terms);
        })
        .catch((thrown: unknown) => {
          if (latestRequestIdRef.current !== requestId) return;
          setError(errorMessage(unwrapError(thrown)));
          onResultsChangeRef.current([], []);
        });
    }, SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasQuery, activeQuery, errorMessage]);

  function reset() {
    setText("");
    setTagFilterId("");
    setAfterDate("");
    setBeforeDate("");
    setIncludeDeleted(false);
    setSelectedSavedSearchId("");
    setSaveName("");
    setError(null);
  }

  useImperativeHandle(ref, () => ({ clear: reset }), []);

  function handleSelectSaved(id: string) {
    setSelectedSavedSearchId(id);
    if (!id) {
      setSaveName("");
      return;
    }
    const saved = savedSearches.find((entry) => entry.id === id);
    if (!saved) return;
    // The saved query string already carries whatever tag:/after:/before:/
    // deleted:true tokens it was created with (store.SavedSearch keeps the
    // query verbatim); loading it into the free-text box and clearing the
    // filter controls avoids composing those tokens a second time.
    setText(saved.query);
    setTagFilterId("");
    setAfterDate("");
    setBeforeDate("");
    setIncludeDeleted(false);
    setSaveName(saved.name);
  }

  async function handleSaveOrUpdate() {
    const name = saveName.trim();
    if (!hasQuery || !name) return;
    setError(null);
    try {
      if (selectedSavedSearchId) {
        await updateSavedSearch(selectedSavedSearchId, name, activeQuery);
      } else {
        const created = await createSavedSearch(name, activeQuery);
        setSelectedSavedSearchId(created.id);
      }
      refreshSavedSearches();
    } catch (thrown: unknown) {
      setError(errorMessage(unwrapError(thrown)));
    }
  }

  async function handleDelete() {
    if (!selectedSavedSearchId) return;
    setError(null);
    try {
      await deleteSavedSearch(selectedSavedSearchId);
      setSelectedSavedSearchId("");
      setSaveName("");
      refreshSavedSearches();
    } catch (thrown: unknown) {
      setError(errorMessage(unwrapError(thrown)));
    }
  }

  return (
    <div className="search-bar">
      <input
        type="search"
        value={text}
        onChange={(event) => setText(event.target.value)}
        placeholder={t("search.placeholder")}
        aria-label={t("search.placeholder")}
      />
      <div className="search-filters">
        <label className="search-filter">
          <span>{t("search.tag_filter_label")}</span>
          <select value={tagFilterId} onChange={(event) => setTagFilterId(event.target.value)}>
            <option value="">{t("search.tag_filter_all")}</option>
            {tags
              .filter((tag) => !tag.deleted)
              .map((tag) => (
                <option key={tag.id} value={tag.id}>
                  {tag.name}
                </option>
              ))}
          </select>
        </label>
        <label className="search-filter">
          <span>{t("search.after_label")}</span>
          <input type="date" value={afterDate} onChange={(event) => setAfterDate(event.target.value)} />
        </label>
        <label className="search-filter">
          <span>{t("search.before_label")}</span>
          <input type="date" value={beforeDate} onChange={(event) => setBeforeDate(event.target.value)} />
        </label>
        <label className="search-filter search-filter-checkbox">
          <input
            type="checkbox"
            checked={includeDeleted}
            onChange={(event) => setIncludeDeleted(event.target.checked)}
          />
          <span>{t("search.include_deleted")}</span>
        </label>
        {hasQuery ? (
          <button type="button" className="link-button" onClick={reset}>
            {t("search.clear_button")}
          </button>
        ) : null}
      </div>

      <div className="saved-searches">
        <select
          aria-label={t("search.saved_searches_label")}
          value={selectedSavedSearchId}
          onChange={(event) => handleSelectSaved(event.target.value)}
        >
          <option value="">{t("search.saved_searches_placeholder")}</option>
          {savedSearches.map((saved) => (
            <option key={saved.id} value={saved.id}>
              {saved.name}
            </option>
          ))}
        </select>
        <input
          type="text"
          value={saveName}
          onChange={(event) => setSaveName(event.target.value)}
          placeholder={t("search.save_new_placeholder")}
          aria-label={t("search.save_new_placeholder")}
        />
        <button
          type="button"
          disabled={!hasQuery || !saveName.trim()}
          onClick={() => void handleSaveOrUpdate()}
        >
          {selectedSavedSearchId ? t("search.update_button") : t("search.save_button")}
        </button>
        {selectedSavedSearchId ? (
          <button type="button" className="link-button" onClick={() => void handleDelete()}>
            {t("search.delete_button")}
          </button>
        ) : null}
      </div>

      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
});
