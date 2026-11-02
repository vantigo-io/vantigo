import { ActionIcon, Indicator, Loader, Tooltip } from "@mantine/core";
import { IconCheck, IconHandStop, IconMessageChatbot } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useAssistantRunStatus } from "../lib/run-status";
import "../i18n";

const shortcutHint = () => {
  try {
    return /Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318J" : "Ctrl+J";
  } catch {
    return "Ctrl+J";
  }
};

export interface AssistantButtonProps {
  /** Whether the panel is open: the button reads as pressed. */
  open: boolean;
  onClick: () => void;
}

/**
 * The Assistant button for the shell header. The host shows it only to a
 * person who has the assistant. A badge says what the run is doing while the
 * panel is shut: a spinner while it works, a hand when a change waits for the
 * person to confirm it, a check once it has answered.
 */
export const AssistantButton = ({ open, onClick }: AssistantButtonProps) => {
  const { t } = useI18n("assistant");
  const status = useAssistantRunStatus();
  const label = open ? t("closeAssistant") : t("openAssistant");
  return (
    <Tooltip label={`${label} (${shortcutHint()})`}>
      <Indicator
        disabled={status === "idle"}
        color={status === "done" ? "teal" : status === "waiting" ? "orange" : "blue"}
        size={status === "working" ? 14 : 12}
        offset={4}
        label={
          status === "working" ? (
            <Loader size={8} color="white" />
          ) : status === "waiting" ? (
            <IconHandStop size={9} stroke={3} />
          ) : (
            <IconCheck size={9} stroke={3} />
          )
        }
        processing={status === "working"}
      >
        <ActionIcon
          variant={open ? "light" : "subtle"}
          color="gray"
          size="lg"
          aria-label={label}
          aria-pressed={open}
          title={
            status === "working"
              ? t("statusWorking")
              : status === "waiting"
                ? t("statusWaiting")
                : status === "done"
                  ? t("statusDone")
                  : undefined
          }
          data-status={status}
          onClick={onClick}
        >
          <IconMessageChatbot size={20} stroke={1.5} />
        </ActionIcon>
      </Indicator>
    </Tooltip>
  );
};
