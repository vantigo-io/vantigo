import { ActionIcon, Box, Group, Loader, Menu, Text, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconExternalLink, IconHistory, IconPlus, IconX } from "@tabler/icons-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n, useSidePanelActions, useSidePanelShown } from "@vantigo/frontend-shell";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  conversationQueryOptions,
  conversationsQueryOptions,
  meQueryKey,
  meQueryOptions,
  putMe,
} from "../api/assistant";
import type { AssistantConversation, AssistantPendingAction } from "../api/types";
import type { AssistantChip } from "../lib/context";
import type { PendingQuestion } from "../lib/panel";
import { setAssistantRunStatus } from "../lib/run-status";
import { ConversationView } from "./conversation-view";
import "../i18n";

export interface AssistantPanelProps {
  /** The screen or record the person is on, for the chip and a new conversation's origin. */
  context?: AssistantChip;
  /** Opens an in-app route. The host's router does it, so the panel stays open. */
  onNavigate: (route: string) => void;
  /** Opens the Assistant app, on a conversation when there is one. */
  onOpenInAssistant: (conversationId: string | null) => void;
  /** A question to send into a fresh conversation (from Spotlight). */
  pendingQuestion?: PendingQuestion;
  onQuestionSent: () => void;
}

/** The recent conversations, in a menu in the header. */
const RecentMenu = ({
  onSelect,
  currentId,
}: {
  onSelect: (conversation: AssistantConversation) => void;
  currentId: string | null;
}) => {
  const { t } = useI18n("assistant");
  const recent = useInfiniteQuery(conversationsQueryOptions({ limit: 8 }));
  const items = recent.data?.pages[0]?.items ?? [];
  return (
    <Menu position="bottom-end" withinPortal width={280}>
      <Menu.Target>
        <Tooltip label={t("recentConversations")}>
          <ActionIcon variant="subtle" color="gray" aria-label={t("recentConversations")}>
            <IconHistory size={18} />
          </ActionIcon>
        </Tooltip>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Label>{t("recentConversations")}</Menu.Label>
        {recent.isPending && (
          <Menu.Item disabled leftSection={<Loader size={12} />}>
            {t("working")}
          </Menu.Item>
        )}
        {recent.isSuccess && items.length === 0 && <Menu.Item disabled>{t("noConversations")}</Menu.Item>}
        {items.map((conversation) => (
          <Menu.Item
            key={conversation.id}
            data-active={conversation.id === currentId || undefined}
            onClick={() => onSelect(conversation)}
          >
            <Text size="sm" lineClamp={1}>
              {conversation.title || t("untitled")}
            </Text>
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  );
};

/**
 * The panel in the shell (design S10): the conversation view with a header of
 * its own. It opens on the conversation the person had open last, remembered
 * on the server so it follows them between devices, shows the context chip for
 * the screen or record they are on, and goes on working when it is closed. The
 * host mounts it in the shell's side panel, so it stays open across apps; it is
 * mounted from the first time it is opened and kept, so a run's progress
 * reaches the button's badge while the panel is shut.
 */
export const AssistantPanel = ({
  context,
  onNavigate,
  onOpenInAssistant,
  pendingQuestion,
  onQuestionSent,
}: AssistantPanelProps) => {
  const { t } = useI18n("assistant");
  // Whether the shared side panel is open with this panel in front; the host owns the width, the tabs and closing.
  const open = useSidePanelShown();
  const { close: onClose } = useSidePanelActions();
  const queryClient = useQueryClient();
  const me = useQuery(meQueryOptions());
  // Opening the panel counts as looking at a finished run: the button's badge clears.
  useEffect(() => {
    if (open) setAssistantRunStatus("idle");
  }, [open]);
  // The person's own choice in this panel; until they make one, the last conversation the server remembers.
  // undefined: no choice yet; null: a fresh conversation; a string: that conversation.
  const [chosen, setChosen] = useState<string | null | undefined>(undefined);
  const remembered = me.isSuccess ? (me.data.lastConversationId ?? null) : me.isError ? null : undefined;
  const selected = chosen !== undefined ? chosen : remembered;

  // A question from Spotlight starts a fresh conversation.
  const [seenQuestion, setSeenQuestion] = useState<number | undefined>();
  if (pendingQuestion && pendingQuestion.id !== seenQuestion) {
    setSeenQuestion(pendingQuestion.id);
    setChosen(null);
  }

  const remember = useCallback(
    (id: string | null) => {
      void putMe(id)
        .then((saved) => queryClient.setQueryData(meQueryKey, saved))
        .catch(() => undefined);
    },
    [queryClient],
  );

  const openConversation = (id: string) => {
    setChosen(id);
    remember(id);
  };

  // What the badge shows: working while a run goes, done when it ended with the panel shut.
  const openRef = useRef(open);
  useLayoutEffect(() => {
    openRef.current = open;
  });
  const waitingRef = useRef(false);
  const onWorkingChange = useCallback((working: boolean) => {
    if (working) setAssistantRunStatus("working");
    else if (waitingRef.current) setAssistantRunStatus("waiting");
    else setAssistantRunStatus(openRef.current ? "idle" : "done");
  }, []);
  // Shut again with a change still waiting: the badge says so.
  useEffect(() => {
    if (!open && waitingRef.current) setAssistantRunStatus("waiting");
  }, [open]);
  // A change waits for the person: the badge says so until they decide it.
  const onWaitingChange = useCallback((waiting: boolean) => {
    waitingRef.current = waiting;
    if (waiting) setAssistantRunStatus("waiting");
    else setAssistantRunStatus(openRef.current ? "idle" : "done");
  }, []);
  // With the panel shut, a proposed change would wait unseen: a notification says so.
  const onProposal = useCallback(
    (card: AssistantPendingAction) => {
      if (openRef.current) return;
      notifications.show({
        id: `assistant-card-${card.id}`,
        color: "orange",
        title: t("notifyWaitingTitle"),
        message: card.explain || card.summary,
        autoClose: false,
      });
    },
    [t],
  );

  const current = selected ?? null;
  const origin = { app: context?.app ?? "home", route: context?.route ?? "/" };
  // The title is read from the cache the conversation view fills; this query fetches nothing.
  const titled = useQuery({ ...conversationQueryOptions(current ?? ""), enabled: false });
  const currentTitle = current ? titled.data?.conversation.title : undefined;

  return (
    <Box
      data-assistant-panel
      role="complementary"
      aria-label={t("panelLabel")}
      // inert keeps a closed (slid-out) panel out of the tab order and the accessibility tree while it stays mounted
      inert={!open}
      h="100%"
      style={{ position: "relative", display: "flex", flexDirection: "column", minHeight: 0, width: "100%" }}
    >
      <Group
        justify="space-between"
        wrap="nowrap"
        gap="xs"
        px="sm"
        py={8}
        style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
      >
        <Text fw={600} size="sm" lineClamp={1} flex={1} data-testid="panel-title">
          {currentTitle || t("assistant")}
        </Text>
        <Group gap={2} wrap="nowrap">
          <RecentMenu currentId={current} onSelect={(conversation) => openConversation(conversation.id)} />
          <Tooltip label={t("newConversation")}>
            <ActionIcon variant="subtle" color="gray" aria-label={t("newConversation")} onClick={() => setChosen(null)}>
              <IconPlus size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label={t("openInAssistant")}>
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t("openInAssistant")}
              onClick={() => onOpenInAssistant(current)}
            >
              <IconExternalLink size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label={t("close")}>
            <ActionIcon variant="subtle" color="gray" aria-label={t("close")} onClick={onClose}>
              <IconX size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      <Box flex={1} style={{ minHeight: 0 }}>
        {selected !== undefined && (
          <ConversationView
            conversationId={current}
            origin={origin}
            context={context}
            onCreated={(id) => {
              setChosen(id);
              remember(id);
            }}
            onMissing={() => setChosen(null)}
            onNavigate={onNavigate}
            onWorkingChange={onWorkingChange}
            onWaitingChange={onWaitingChange}
            onProposal={onProposal}
            question={pendingQuestion && current === null ? pendingQuestion : undefined}
            onQuestionSent={onQuestionSent}
          />
        )}
      </Box>
    </Box>
  );
};
