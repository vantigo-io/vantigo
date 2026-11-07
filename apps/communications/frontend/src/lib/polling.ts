import { useEffect, useRef } from "react";

/**
 * The fallback poll, while a screen's live topic is not live (live design L13, L19): the
 * same minute main's Inbox uses. The queries stop polling by themselves in a hidden tab.
 */
export const FALLBACK_POLL_MS = 60_000;

/** Calls `refetch` when `active` turns from false to true, not on the first render. */
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
