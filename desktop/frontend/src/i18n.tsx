import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { getSettings, localeCatalog, updateSettings, type ApiError } from "./api";

export const SUPPORTED_LOCALES = ["en", "ru"] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];

interface I18nContextValue {
  locale: Locale;
  /** t looks up key in the current catalog, falling back to the key
   * itself so a missing translation is visibly wrong rather than blank. */
  t: (key: string) => string;
  /** errorMessage localizes an ApiError.code via the "errors.<code>"
   * catalog key, falling back to "errors.internal" for an unrecognized
   * code (see api.ts's ApiError doc comment: Message is never shown to
   * the user directly, since it is English-only backend diagnostic
   * text). */
  errorMessage: (error: ApiError) => string;
  setLocale: (locale: Locale) => Promise<void>;
  ready: boolean;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>("en");
  const [strings, setStrings] = useState<Record<string, string>>({});
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let canceled = false;
    (async () => {
      const settings = await getSettings();
      const initial = isSupportedLocale(settings.language) ? settings.language : "en";
      const catalog = await localeCatalog(initial);
      if (canceled) return;
      setLocaleState(initial);
      setStrings(catalog.strings);
      setReady(true);
    })().catch(() => {
      // No settings/catalog yet is not fatal: English strings loaded via
      // setLocale below still let the app proceed with a visible language
      // toggle rather than a blank screen.
      if (!canceled) setReady(true);
    });
    return () => {
      canceled = true;
    };
  }, []);

  const setLocale = useCallback(async (next: Locale) => {
    const catalog = await localeCatalog(next);
    setStrings(catalog.strings);
    setLocaleState(next);
    const current = await getSettings();
    await updateSettings({ ...current, language: next });
  }, []);

  const t = useCallback((key: string) => strings[key] ?? key, [strings]);

  const errorMessage = useCallback(
    (error: ApiError) => strings[`errors.${error.code}`] ?? strings["errors.internal"] ?? error.message,
    [strings],
  );

  const value = useMemo<I18nContextValue>(
    () => ({ locale, t, errorMessage, setLocale, ready }),
    [locale, t, errorMessage, setLocale, ready],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error("useI18n must be used within an I18nProvider");
  }
  return ctx;
}

function isSupportedLocale(value: string): value is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value);
}
