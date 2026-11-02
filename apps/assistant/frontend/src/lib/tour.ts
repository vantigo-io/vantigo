import { useSyncExternalStore } from "react";
import type { AssistantTourStep } from "../api/types";

/**
 * The guided tour the person has asked for (design S11). The conversation view
 * starts it from a "Show me" button and the runner, mounted once in the shell,
 * plays it, so a tour can cross routes and survive the panel closing. Nothing
 * here starts by itself: only `startTour`, which a click calls, begins one.
 */
export interface ActiveTour {
  /** A new tour has a new run, so asking for the same one again restarts it. */
  run: number;
  steps: readonly AssistantTourStep[];
  /** What had the focus when the tour began (the button), to hand it back on close. */
  returnFocus: HTMLElement | null;
}

let current: ActiveTour | null = null;
let runs = 0;
const listeners = new Set<() => void>();

const emit = () => {
  for (const listener of listeners) listener();
};

export const startTour = (steps: readonly AssistantTourStep[], returnFocus?: HTMLElement | null) => {
  if (steps.length === 0) return;
  runs += 1;
  current = { run: runs, steps, returnFocus: returnFocus ?? null };
  emit();
};

export const endTour = () => {
  if (current === null) return;
  current = null;
  emit();
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const useActiveTour = (): ActiveTour | null =>
  useSyncExternalStore(
    subscribe,
    () => current,
    () => null,
  );

/** How long a step waits for its control before it shows centred. */
export const TOUR_TARGET_WAIT_MS = 3000;

/**
 * Resolves true when something matches `selector`, as soon as it mounts, or
 * false after `ms`. A step's control may mount after the route changes (the page
 * loads) or after the person opens the dialog it lives in.
 */
export const waitForElement = (selector: string, ms: number = TOUR_TARGET_WAIT_MS, signal?: AbortSignal) =>
  new Promise<boolean>((resolve) => {
    if (document.querySelector(selector)) {
      resolve(true);
      return;
    }
    let done = false;
    const finish = (found: boolean) => {
      if (done) return;
      done = true;
      observer.disconnect();
      clearTimeout(timer);
      resolve(found);
    };
    const observer = new MutationObserver(() => {
      if (document.querySelector(selector)) finish(true);
    });
    observer.observe(document.body, { childList: true, subtree: true });
    const timer = setTimeout(() => finish(false), ms);
    signal?.addEventListener("abort", () => finish(false), { once: true });
  });
