import { useSyncExternalStore } from "react";

/**
 * Which sidebar articles are open, kept across the app's pages (they each
 * mount the sidebar) and, best effort, across visits in this browser.
 */
const STORAGE_KEY = "knowledge.sidebar.expanded";

const read = (): Set<string> => {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const ids = raw ? (JSON.parse(raw) as unknown) : [];
    return new Set(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string") : []);
  } catch {
    return new Set();
  }
};

let expanded: ReadonlySet<string> = read();
const listeners = new Set<() => void>();

const write = (next: Set<string>) => {
  expanded = next;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...next]));
  } catch {
    // Storage may be full or blocked; the sidebar still opens for this visit.
  }
  for (const listener of listeners) listener();
};

export const setExpanded = (id: string, open: boolean) => {
  if (expanded.has(id) === open) return;
  const next = new Set(expanded);
  if (open) next.add(id);
  else next.delete(id);
  write(next);
};

/** Opens every article on a path, so the open article shows in the tree. */
export const expandAll = (ids: readonly string[]) => {
  if (ids.every((id) => expanded.has(id))) return;
  write(new Set([...expanded, ...ids]));
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const useExpanded = (): ReadonlySet<string> => useSyncExternalStore(subscribe, () => expanded);

/** Test seam: forget what is open. */
export const resetExpanded = () => write(new Set());
