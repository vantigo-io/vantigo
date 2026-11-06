import type { SidePanelController } from "@vantigo/frontend-shell";
import { useCallback, useRef, useState } from "react";

/** A question the person asked from outside the panel (Spotlight), sent when the panel has opened. */
export interface PendingQuestion {
  /** Distinguishes a repeated question from the one before. */
  id: number;
  text: string;
}

export const ASSISTANT_PANEL_KEY = "assistant";

/**
 * The Assistant's part of the shell's side panel, owned by the shell layout so
 * it survives navigation between apps: whether it is in front, to show or hide
 * it, and a question waiting to be sent. Whether the panel is open, its width,
 * the mode and the mounting belong to the shared side panel
 * (`useSidePanelController`); opening the Assistant counts as looking at a
 * finished run, which the panel itself clears.
 */
export const useAssistantPanel = (side: Pick<SidePanelController, "show" | "close" | "toggle" | "isShown">) => {
  const [pending, setPending] = useState<PendingQuestion | undefined>();
  const { show, close, toggle } = side;
  const open = side.isShown(ASSISTANT_PANEL_KEY);

  const setOpen = useCallback(
    (next: boolean) => {
      if (next) show(ASSISTANT_PANEL_KEY);
      else close();
    },
    [show, close],
  );
  const toggleAssistant = useCallback(() => toggle(ASSISTANT_PANEL_KEY), [toggle]);

  const nextId = useRef(0);
  /** Opens the panel with this question to be sent. */
  const ask = useCallback(
    (text: string) => {
      nextId.current += 1;
      setPending({ id: nextId.current, text });
      show(ASSISTANT_PANEL_KEY);
    },
    [show],
  );
  const consumeQuestion = useCallback(() => setPending(undefined), []);

  return { open, setOpen, toggle: toggleAssistant, pending, ask, consumeQuestion };
};

export type AssistantPanelController = ReturnType<typeof useAssistantPanel>;
