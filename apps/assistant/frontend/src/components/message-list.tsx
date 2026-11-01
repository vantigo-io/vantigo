import { Box, Button, Group, Loader, Paper, Stack, Text } from "@mantine/core";
import { IconCheck, IconPlayerStop } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { AssistantMessage, AssistantMessageContext, AssistantPart } from "../api/types";
import { textOf, visibleMessages } from "../lib/messages";
import { Markdown } from "./markdown";

export interface MessageListProps {
  messages: readonly AssistantMessage[];
  /** The person's message before the server has answered with its own. */
  pendingUser?: { text: string; context?: AssistantMessageContext };
  /** The assistant's text so far in the run being streamed. */
  partial: string;
  /** The tool the run is using now, in the person's language. */
  tool?: string;
  working: boolean;
  stopped: boolean;
  onNavigate?: (route: string) => void;
}

/** One line saying what the assistant did to answer: "Searching the documentation". */
const ToolLine = ({ label, running = false }: { label: string; running?: boolean }) => (
  <Group gap={6} wrap="nowrap" c="dimmed" data-testid="tool-line">
    {running ? <Loader size={12} /> : <IconCheck size={14} stroke={1.5} />}
    <Text size="xs" c="dimmed">
      {label}
    </Text>
  </Group>
);

const UserBubble = ({ text, label }: { text: string; label?: string }) => {
  const { t } = useI18n("assistant");
  return (
    <Stack gap={2} align="flex-end" data-role="user">
      <Paper bg="var(--mantine-primary-color-light)" px="sm" py={6} radius="md" maw="90%">
        <Text size="sm" style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
          {text}
        </Text>
      </Paper>
      {label && (
        <Text size="xs" c="dimmed">
          {t("viewing", { label })}
        </Text>
      )}
    </Stack>
  );
};

const AssistantParts = ({
  parts,
  onNavigate,
}: {
  parts: readonly AssistantPart[];
  onNavigate?: (route: string) => void;
}) => {
  const { t } = useI18n("assistant");
  return (
    <Stack gap="xs" data-role="assistant">
      {parts.map((part, index) => {
        const key = `${part.type}-${part.callId ?? index}-${index}`;
        if (part.type === "tool_call") return <ToolLine key={key} label={part.label ?? part.name ?? ""} />;
        if (part.type === "text") {
          const text = part.text ?? part.content ?? "";
          return text ? (
            <Markdown key={key} onNavigate={onNavigate}>
              {text}
            </Markdown>
          ) : null;
        }
        if (part.type === "link" && part.route)
          return (
            <Box key={key}>
              <Button size="xs" variant="light" onClick={() => onNavigate?.(part.route ?? "")}>
                {part.label || t("takeMeThere")}
              </Button>
            </Box>
          );
        return null;
      })}
    </Stack>
  );
};

/**
 * The conversation as the person reads it. User and assistant messages only:
 * a tool-role message is the model's own business. An assistant message shows
 * its tool calls as status lines, its text as Markdown (no raw HTML) and a
 * `link` part as a "Take me there" button. While a run is going the streamed
 * text and the tool in use follow the last message.
 */
export const MessageList = ({
  messages,
  pendingUser,
  partial,
  tool,
  working,
  stopped,
  onNavigate,
}: MessageListProps) => {
  const { t } = useI18n("assistant");
  return (
    <Stack gap="md" p="md" aria-live="polite" aria-busy={working}>
      {visibleMessages(messages).map((message) =>
        message.role === "user" ? (
          <UserBubble key={message.id} text={textOf(message)} label={message.context?.label} />
        ) : (
          <AssistantParts key={message.id} parts={message.parts} onNavigate={onNavigate} />
        ),
      )}
      {pendingUser && <UserBubble text={pendingUser.text} label={pendingUser.context?.label} />}
      {partial && (
        <Box data-testid="streaming">
          <Markdown onNavigate={onNavigate}>{partial}</Markdown>
        </Box>
      )}
      {working && (
        <Group gap={6} c="dimmed" wrap="nowrap" role="status">
          <Loader size={12} />
          <Text size="xs" c="dimmed">
            {tool ?? t("working")}
          </Text>
        </Group>
      )}
      {stopped && !working && (
        <Group gap={6} c="dimmed" wrap="nowrap">
          <IconPlayerStop size={14} stroke={1.5} />
          <Text size="xs" c="dimmed">
            {t("stopped")}
          </Text>
        </Group>
      )}
    </Stack>
  );
};
