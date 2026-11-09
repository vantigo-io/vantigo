import { Alert, Box, Button, Center, Group, Loader, ScrollArea, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import { IconMessageChatbot } from "@tabler/icons-react";
import { UserAvatar, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useRef, useState } from "react";
import type { AssistantPendingAction } from "../api/types";
import { useConversation } from "../hooks/use-conversation";
import type { AssistantChip } from "../lib/context";
import { visibleMessages } from "../lib/messages";
import { AgentPicker } from "./agent-picker";
import { Composer } from "./composer";
import { MessageList } from "./message-list";

/** A question waiting to be sent into a fresh conversation (the Spotlight's "Ask the assistant"). */
export interface InitialQuestion {
  id: number;
  text: string;
}

export interface ConversationViewProps {
  /** The open conversation; null is a draft, created when the first message is sent. */
  conversationId: string | null;
  origin: { app: string; route: string };
  /** The chip: the screen or record the person is on. Undefined shows no chip. */
  context?: AssistantChip;
  onCreated: (id: string) => void;
  onMissing?: () => void;
  onNavigate?: (route: string) => void;
  onWorkingChange?: (working: boolean) => void;
  /** A change waits for the person to confirm it (for the button's badge). */
  onWaitingChange?: (waiting: boolean) => void;
  /** A change was proposed while this view listened. */
  onProposal?: (card: AssistantPendingAction) => void;
  /** Sent once, as soon as the conversation is a draft and idle. */
  question?: InitialQuestion;
  onQuestionSent?: (id: number) => void;
}

const Empty = () => {
  const { t } = useI18n("assistant");
  return (
    <Center h="100%" p="xl">
      <Stack align="center" gap="xs" maw={420}>
        <ThemeIcon size={48} radius="xl" variant="light">
          <IconMessageChatbot size={28} stroke={1.5} />
        </ThemeIcon>
        <Title order={4}>{t("emptyTitle")}</Title>
        <Text size="sm" c="dimmed" ta="center">
          {t("emptyBody")}
        </Text>
      </Stack>
    </Center>
  );
};

/**
 * The messages and the composer of one conversation: the part the panel and
 * the Assistant app share.
 */
export const ConversationView = ({
  conversationId,
  origin,
  context,
  onCreated,
  onMissing,
  onNavigate,
  onWorkingChange,
  onWaitingChange,
  onProposal,
  question,
  onQuestionSent,
}: ConversationViewProps) => {
  const { t } = useI18n("assistant");
  // Who a draft is with (V31); once it is sent, the conversation says.
  const [draftAgent, setDraftAgent] = useState<string | null>(null);
  const conversation = useConversation({
    conversationId,
    origin,
    agentId: draftAgent,
    onCreated,
    onMissing,
    onWorkingChange,
    onWaitingChange,
    onProposal,
  });
  const {
    detail,
    messages,
    pendingUser,
    partial,
    tool,
    working,
    waiting,
    cards,
    freshCard,
    deciding,
    decisionErrors,
    stopped,
    error,
    send,
    confirm,
    cancel,
    continueJob,
    stopJob,
    stop,
    clearError,
  } = conversation;

  // A question from outside the panel is sent once, into a fresh conversation.
  const sentQuestion = useRef<number | undefined>(undefined);
  useEffect(() => {
    if (!question || conversationId !== null || working || sentQuestion.current === question.id) return;
    sentQuestion.current = question.id;
    void send(question.text, context);
    onQuestionSent?.(question.id);
  }, [question, conversationId, working, send, context, onQuestionSent]);

  const bottom = useRef<HTMLDivElement>(null);
  const shown = visibleMessages(messages).length;
  useEffect(() => {
    bottom.current?.scrollIntoView?.({ block: "end" });
  }, [shown, partial, working, pendingUser, tool]);

  const agent = conversationId !== null ? detail.data?.conversation.agent : undefined;
  const loading = conversationId !== null && detail.isPending;
  const failed = conversationId !== null && detail.isError && (detail.error as { status?: number }).status !== 404;
  const empty = !loading && shown === 0 && !pendingUser && !working && !stopped;

  return (
    <Stack gap={0} h="100%" style={{ minHeight: 0 }}>
      {agent && (
        <Group
          gap="xs"
          wrap="nowrap"
          px="sm"
          py={6}
          data-testid="agent-header"
          style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
        >
          <UserAvatar size="sm" name={agent.name} kind="agent" />
          <Box style={{ minWidth: 0 }}>
            <Text size="sm" fw={600} lineClamp={1}>
              {agent.name}
            </Text>
            <Text size="xs" c="dimmed">
              {t("agentConversationNote", { name: agent.name })}
            </Text>
          </Box>
        </Group>
      )}
      <ScrollArea flex={1} type="auto" style={{ minHeight: 0 }}>
        {loading && (
          <Center p="xl">
            <Loader size="sm" />
          </Center>
        )}
        {failed && (
          <Alert color="red" m="md" title={t("loadFailed")}>
            <Button size="xs" variant="light" onClick={() => void detail.refetch()}>
              {t("tryAgain")}
            </Button>
          </Alert>
        )}
        {empty && !failed && <Empty />}
        {!loading && !empty && (
          <MessageList
            messages={messages}
            pendingUser={pendingUser}
            partial={partial}
            tool={tool}
            working={working}
            stopped={stopped}
            onNavigate={onNavigate}
            cards={cards}
            freshCard={freshCard}
            decidingCard={deciding}
            decisionErrors={Object.fromEntries(Object.entries(decisionErrors).map(([id, key]) => [id, t(key)]))}
            onConfirm={(card) => void confirm(card)}
            onCancel={(card) => void cancel(card)}
            onContinue={(card) => void continueJob(card)}
            onStop={(card) => void stopJob(card)}
          />
        )}
        {error && (
          <Alert color="red" mx="md" mb="md" withCloseButton onClose={clearError}>
            {t(error)}
          </Alert>
        )}
        <Box ref={bottom} />
      </ScrollArea>
      {conversationId === null && !working && <AgentPicker value={draftAgent} onChange={setDraftAgent} />}
      <Composer
        context={context}
        working={working}
        waiting={waiting}
        onSend={(text, chip) => void send(text, chip)}
        onStop={() => void stop()}
      />
    </Stack>
  );
};
