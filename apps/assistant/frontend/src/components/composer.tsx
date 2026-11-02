import { ActionIcon, CloseButton, Group, Stack, Text, Textarea, Tooltip } from "@mantine/core";
import { IconPlayerStopFilled, IconSend } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { AssistantChip } from "../lib/context";

export interface ComposerProps {
  /** The screen or record the person is on; undefined where there is none (the app page). */
  context?: AssistantChip;
  working: boolean;
  /** A proposed change waits for the person: the box is off until they decide it. */
  waiting?: boolean;
  onSend: (text: string, context?: AssistantChip) => void;
  onStop: () => void;
}

/**
 * The message box with the context chip above it ("Viewing: Customer Ola
 * Nordmann"). Removing the chip withholds the context from the next message
 * only; it follows the person to the next screen. Enter sends, Shift+Enter
 * starts a new line, and while a run is going Send becomes Stop.
 */
export const Composer = ({ context, working, waiting = false, onSend, onStop }: ComposerProps) => {
  const { t } = useI18n("assistant");
  const [text, setText] = useState("");
  const [dismissedFor, setDismissedFor] = useState<string | undefined>();
  const contextKey = context ? `${context.route}|${context.label}` : undefined;
  const chip = context && dismissedFor !== contextKey ? context : undefined;

  // The chip follows navigation: a new screen brings it back.
  const [seenKey, setSeenKey] = useState(contextKey);
  if (seenKey !== contextKey) {
    setSeenKey(contextKey);
    setDismissedFor(undefined);
  }

  const submit = () => {
    const trimmed = text.trim();
    if (!trimmed || working || waiting) return;
    onSend(trimmed, chip);
    setText("");
    // Removing the chip is for the next message only.
    setDismissedFor(undefined);
  };

  return (
    <Stack gap={6} p="sm" style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}>
      {chip && (
        <Group
          gap={4}
          wrap="nowrap"
          px="xs"
          py={2}
          data-testid="context-chip"
          style={{
            alignSelf: "flex-start",
            maxWidth: "100%",
            borderRadius: "var(--mantine-radius-xl)",
            background: "var(--mantine-color-default-hover)",
          }}
        >
          <Text size="xs" lineClamp={1}>
            {t("viewing", { label: chip.label })}
          </Text>
          <CloseButton size="xs" aria-label={t("removeContext")} onClick={() => setDismissedFor(contextKey)} />
        </Group>
      )}
      <Group gap="xs" align="flex-end" wrap="nowrap">
        <Textarea
          flex={1}
          autosize
          minRows={1}
          maxRows={6}
          maxLength={8000}
          aria-label={t("composerLabel")}
          placeholder={waiting ? t("composerWaiting") : t("composerPlaceholder")}
          disabled={waiting}
          value={text}
          onChange={(event) => setText(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault();
              submit();
            }
          }}
        />
        {working ? (
          <Tooltip label={t("stop")}>
            <ActionIcon size="lg" color="red" variant="filled" aria-label={t("stop")} onClick={onStop}>
              <IconPlayerStopFilled size={16} />
            </ActionIcon>
          </Tooltip>
        ) : (
          <Tooltip label={t("send")}>
            <ActionIcon size="lg" aria-label={t("send")} disabled={waiting || text.trim() === ""} onClick={submit}>
              <IconSend size={16} />
            </ActionIcon>
          </Tooltip>
        )}
      </Group>
    </Stack>
  );
};
