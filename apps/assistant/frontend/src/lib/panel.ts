import { useCallback, useRef, useState } from "react";
import { setAssistantRunStatus } from "./run-status";

export const PANEL_MIN_WIDTH = 320;
export const PANEL_MAX_WIDTH = 720;
export const PANEL_DEFAULT_WIDTH = 400;
const WIDTH_KEY = "vantigo.assistant.panelWidth";

export const clampPanelWidth = (width: number) =>
  Math.min(PANEL_MAX_WIDTH, Math.max(PANEL_MIN_WIDTH, Math.round(width)));

/** The width kept in this browser; storage can be missing, full or blocked, and the default stands then. */
export const readPanelWidth = (): number => {
  try {
    const stored = Number(window.localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(stored) && stored > 0 ? clampPanelWidth(stored) : PANEL_DEFAULT_WIDTH;
  } catch {
    return PANEL_DEFAULT_WIDTH;
  }
};

export const writePanelWidth = (width: number) => {
  try {
    window.localStorage.setItem(WIDTH_KEY, String(clampPanelWidth(width)));
  } catch {
    /* the width is a convenience; losing it is fine */
  }
};

/** A question the person asked from outside the panel (Spotlight), sent when the panel has opened. */
export interface PendingQuestion {
  /** Distinguishes a repeated question from the one before. */
  id: number;
  text: string;
}

/**
 * The panel's state, owned by the shell layout so it survives navigation
 * between apps: whether it is open (it starts closed and that is not
 * remembered), its width (remembered in this browser) and a question waiting
 * to be sent. Opening it counts as looking at a finished run, so the button's
 * `done` badge clears.
 */
export const useAssistantPanel = () => {
  const [open, setOpenState] = useState(false);
  const [width, setWidthState] = useState(readPanelWidth);
  const [pending, setPending] = useState<PendingQuestion | undefined>();
  const [mounted, setMounted] = useState(false);

  const setOpen = useCallback((next: boolean) => {
    if (next) {
      setMounted(true);
      setAssistantRunStatus("idle");
    }
    setOpenState(next);
  }, []);

  const toggle = useCallback(() => setOpen(!open), [open, setOpen]);

  const setWidth = useCallback((next: number) => {
    const clamped = clampPanelWidth(next);
    setWidthState(clamped);
    writePanelWidth(clamped);
  }, []);

  const nextId = useRef(0);
  /** Opens the panel with this question to be sent. */
  const ask = useCallback(
    (text: string) => {
      nextId.current += 1;
      setPending({ id: nextId.current, text });
      setOpen(true);
    },
    [setOpen],
  );
  const consumeQuestion = useCallback(() => setPending(undefined), []);

  return { open, setOpen, toggle, width, setWidth, pending, ask, consumeQuestion, mounted };
};

export type AssistantPanelController = ReturnType<typeof useAssistantPanel>;
