import { useEffect, useRef, useSyncExternalStore } from "react";

/** How often the open list refetches while it is on screen (Messenger design N26). */
export const LIST_POLL_MS = 15_000;
/** How often the open conversation refetches while it is on screen. */
export const CONVERSATION_POLL_MS = 5_000;
/** How often the Conversations button reads the counts while the tab is visible. */
export const COUNTS_POLL_MS = 30_000;

const subscribe = (notify: () => void) => {
  document.addEventListener("visibilitychange", notify);
  return () => document.removeEventListener("visibilitychange", notify);
};

/** Whether the browser tab is visible (`document.visibilityState`). */
export const usePageVisible = () =>
  useSyncExternalStore(
    subscribe,
    () => document.visibilityState !== "hidden",
    () => true,
  );

/**
 * The refetch interval for a query that is only worth keeping current while it is
 * on screen: `ms` while `active` and the tab is visible, else false.
 */
export const usePollInterval = (ms: number, active: boolean): number | false => {
  const visible = usePageVisible();
  return active && visible ? ms : false;
};

/** Calls `refetch` when `active` turns from false to true, not on the first render: polling resumes with a refetch. */
export const useRefetchOnResume = (active: boolean, refetch: () => void) => {
  const was = useRef(active);
  const latest = useRef(refetch);
  useEffect(() => {
    latest.current = refetch;
  });
  useEffect(() => {
    if (active && !was.current) latest.current();
    was.current = active;
  }, [active]);
};
