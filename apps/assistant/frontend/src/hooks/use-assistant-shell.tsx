import { useEffect } from "react";
import { AssistantButton } from "../components/assistant-button";
import { AssistantPanel } from "../components/assistant-panel";
import type { AssistantChip } from "../lib/context";
import { useAssistantPanel } from "../lib/panel";
import { useAssistantShortcut } from "../lib/shortcut";
import { useAssistantAvailability } from "./use-availability";

export interface UseAssistantShellOptions {
  /** The host's own gate: signed in, not held at MFA enrolment, the module on. */
  enabled: boolean;
  /** The Assistant app is on screen: it shows the conversations itself, so the panel and its shortcut stand down. */
  onAssistantPage?: boolean;
  /** The screen or record the person is on. */
  context?: AssistantChip;
  /** Opens an in-app route through the host's router. */
  onNavigate: (route: string) => void;
  /** Opens the Assistant app, on a conversation when there is one. */
  onOpenInAssistant: (conversationId: string | null) => void;
}

/**
 * Everything the shell needs of the assistant, in one hook: whether the person
 * has it (so the button and the panel exist at all), the header button, the
 * panel for the layout's aside, the Cmd/Ctrl+J shortcut and `ask`, which opens
 * the panel with a question to send (Spotlight's fallback). The panel's state
 * lives where this hook is called, so call it from the layout that persists
 * across apps.
 */
export const useAssistantShell = ({
  enabled,
  onAssistantPage = false,
  context,
  onNavigate,
  onOpenInAssistant,
}: UseAssistantShellOptions) => {
  const availability = useAssistantAvailability(enabled);
  const panel = useAssistantPanel();
  const available = availability.available;
  const visible = available && !onAssistantPage;
  useAssistantShortcut(panel.toggle, visible);

  const { open, setOpen } = panel;
  useEffect(() => {
    if (!visible && open) setOpen(false);
  }, [visible, open, setOpen]);

  return {
    /** The person has the assistant: Spotlight may offer to ask it, the Assistant app is offered. */
    available,
    /** The panel can be opened from where the person is (the Assistant app shows the conversations itself). */
    visible,
    /** The provider runs on this installation. */
    local: availability.local,
    panel,
    /** For `headerActions`; null when the person does not have the assistant. */
    button: visible ? <AssistantButton open={panel.open} onClick={panel.toggle} /> : null,
    /** For `AppShellLayout`'s `aside`; undefined until the panel is first opened. */
    aside:
      visible && panel.mounted
        ? {
            opened: panel.open,
            width: panel.width,
            children: (
              <AssistantPanel
                open={panel.open}
                width={panel.width}
                onWidthChange={panel.setWidth}
                onClose={() => panel.setOpen(false)}
                context={context}
                onNavigate={onNavigate}
                onOpenInAssistant={(id) => {
                  panel.setOpen(false);
                  onOpenInAssistant(id);
                }}
                pendingQuestion={panel.pending}
                onQuestionSent={panel.consumeQuestion}
              />
            ),
          }
        : undefined,
    /** Opens the panel with this question sent into a fresh conversation. */
    ask: panel.ask,
  };
};
