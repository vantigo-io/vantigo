import { useCallback, useState } from "react";
import { RENAMED_KINDS } from "./colours";

/** Where the legend keeps the kinds the person hid — a convenience per browser, not a setting. */
export const HIDDEN_KINDS_STORAGE_KEY = "vantigo.calendar.hiddenKinds";

/** The kinds the person hid, as saved; a kind renamed since (RENAMED_KINDS) is read as its new name. */
export const readHiddenKinds = (): string[] => {
  try {
    const raw = window.localStorage.getItem(HIDDEN_KINDS_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(parsed)) return [];
    const kinds = parsed
      .filter((kind): kind is string => typeof kind === "string")
      .map((kind) => (Object.hasOwn(RENAMED_KINDS, kind) ? RENAMED_KINDS[kind] : kind));
    return [...new Set(kinds)];
  } catch {
    return [];
  }
};

export const writeHiddenKinds = (kinds: readonly string[]): void => {
  try {
    window.localStorage.setItem(HIDDEN_KINDS_STORAGE_KEY, JSON.stringify(kinds));
  } catch {
    // A private window or blocked storage: the filter works for this visit only.
  }
};

/** The hidden kinds, and a toggle that remembers the choice. */
export const useHiddenKinds = (): [ReadonlySet<string>, (kind: string) => void] => {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set(readHiddenKinds()));
  const toggle = useCallback((kind: string) => {
    setHidden((current) => {
      const next = new Set(current);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      writeHiddenKinds([...next]);
      return next;
    });
  }, []);
  return [hidden, toggle];
};
