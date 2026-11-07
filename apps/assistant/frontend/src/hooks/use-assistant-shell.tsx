import { Box } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { IconMessageChatbot } from "@tabler/icons-react";
import { type AssistTarget, type SidePanel, type SidePanelController, useI18n } from "@vantigo/frontend-shell";
import { AssistantButton } from "../components/assistant-button";
import { AssistantPanel } from "../components/assistant-panel";
import { TourRunner } from "../components/tour-runner";
import type { AssistantChip } from "../lib/context";
import { ASSISTANT_PANEL_KEY, useAssistantPanel } from "../lib/panel";
import { useAssistantRunStatus } from "../lib/run-status";
import { useAssistantShortcut } from "../lib/shortcut";
import { useAssistantAvailability } from "./use-availability";

export interface UseAssistantShellOptions {
  /** The shell's shared side panel; the Assistant is one entry in it (Messenger design N24). */
  side: SidePanelController;
  /** The host's own gate: signed in, not held at MFA enrolment, the module on. */
  enabled: boolean;
  /** The Assistant app is on screen: it shows the conversations itself, so the panel and its shortcut stand down. */
  onAssistantPage?: boolean;
  /** The screen or record the person is on. */
  context?: AssistantChip;
  /** Opens an in-app route through the host's router. */
  onNavigate: (route: string) => void;
  /** Every module frontend's tour targets (`assistTargets`), collected by the host for the guided tours. */
  tourTargets?: readonly AssistTarget[];
  /** Opens the Assistant app, on a conversation when there is one. */
  onOpenInAssistant: (conversationId: string | null) => void;
}

/**
 * Everything the shell needs of the assistant, in one hook: whether the person
 * has it (so the button and the panel exist at all), the header button, the
 * panel's entry for the shared side panel (`sidePanel`), the Cmd/Ctrl+J
 * shortcut and `ask`, which opens the panel with a question to send
 * (Spotlight's fallback). The state lives where this hook is called, so call
 * it from the layout that persists across apps.
 */
export const useAssistantShell = ({
  side,
  enabled,
  onAssistantPage = false,
  context,
  onNavigate,
  tourTargets = [],
  onOpenInAssistant,
}: UseAssistantShellOptions) => {
  const availability = useAssistantAvailability(enabled);
  const { t } = useI18n("assistant");
  const status = useAssistantRunStatus();
  const panel = useAssistantPanel(side);
  const available = availability.available;
  const visible = available && !onAssistantPage;
  useAssistantShortcut(panel.toggle, visible);

  // Below the shell's "sm" breakpoint the panel is a full-screen sheet and would cover the controls a tour points at.
  const sheet = useMediaQuery("(max-width: 48em)") === true;

  return {
    /** The person has the assistant: Spotlight may offer to ask it, the Assistant app is offered. */
    available,
    /** The panel can be opened from where the person is (the Assistant app shows the conversations itself). */
    visible,
    /** The provider runs on this installation. */
    local: availability.local,
    panel,
    /** The guided-tour runner (design S11): render it once in the layout, whatever the page. Null when the person does not have the assistant. */
    tour: available ? (
      <TourRunner
        targets={tourTargets}
        onNavigate={onNavigate}
        onStart={() => {
          if (sheet) side.close();
        }}
      />
    ) : null,
    /** For `headerActions`; null when the person does not have the assistant. */
    button: visible ? <AssistantButton open={panel.open} onClick={panel.toggle} /> : null,
    /** The Assistant's entry for the side panel; undefined when the person does not have the assistant. Hidden on the Assistant's own page. */
    sidePanel: available
      ? ({
          key: ASSISTANT_PANEL_KEY,
          label: t("assistant"),
          icon: <IconMessageChatbot size={16} stroke={1.5} />,
          badge:
            status === "idle" ? undefined : (
              <Box
                w={8}
                h={8}
                data-status={status}
                style={{
                  borderRadius: "50%",
                  background: `var(--mantine-color-${status === "done" ? "teal" : status === "waiting" ? "orange" : "blue"}-6)`,
                }}
              />
            ),
          badgeLabel:
            status === "working"
              ? t("statusWorking")
              : status === "waiting"
                ? t("statusWaiting")
                : status === "done"
                  ? t("statusDone")
                  : undefined,
          hidden: !visible,
          render: () => (
            <AssistantPanel
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
        } satisfies SidePanel)
      : undefined,
    /** Opens the panel with this question sent into a fresh conversation. */
    ask: panel.ask,
  };
};
