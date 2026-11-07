import { ActionIcon, Alert, Anchor, Box, Chip, Group, Stack, Text, TextInput, Tooltip } from "@mantine/core";
import { IconArrowLeft, IconExternalLink, IconX } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n, useSidePanelActions, useSidePanelShown } from "@vantigo/frontend-shell";
import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  type ConversationFilters,
  type ConversationListItem,
  conversationQueryOptions,
  conversationsQueryOptions,
  inboxCountsQueryOptions,
  type Page,
  tagsQueryOptions,
} from "../api/conversations";
import { useConversationActions } from "../hooks/useConversationActions";
import { CONVERSATION_POLL_MS, LIST_POLL_MS, usePageVisible, useRefetchOnResume } from "../lib/polling";
import type { ConnectionButtonProps } from "./ConversationComposer";
import { ConversationList } from "./ConversationList";
import { ConversationThread } from "./ConversationThread";
import { matchesSearch } from "./conversationHelpers";
import "../i18n";
import "../styles.css";

export type ConversationsPanelProps = {
  /** The signed-in person, for the Assigned to me filter. */
  userId?: string;
  /** The customer the main page shows, when it is a customer page: offered as the This customer filter, never forced. */
  customerId?: number;
  /** communications:conversations-reply; without it the composer is read-only. */
  canReply: boolean;
  /** communications:conversations-manage; without it the menus that close, tag and confirm a customer are gone. */
  canManage: boolean;
  /** Opens an in-app route in the main page through the host's router; the panel stays as it is. */
  onNavigate: (route: string) => void;
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
};

type Filter = "mine" | "unread" | "all" | "customer";
const PAGE_SIZE = 30;

const filtersFor = (
  filter: Filter,
  userId: string | undefined,
  customerId: number | undefined,
): ConversationFilters => {
  const base = { page: 1, pageSize: PAGE_SIZE };
  switch (filter) {
    case "mine":
      return { ...base, status: "open", assignedUserId: userId };
    case "unread":
      return { ...base, status: "open", unreadOnly: true };
    case "customer":
      return { ...base, customerId };
    default:
      return { ...base, status: "open" };
  }
};

/**
 * The Conversations panel for the shell's side panel (Messenger design N25): a
 * compact list with the filters Assigned to me, Unread and All open (and This
 * customer on a customer page) and a search over what is loaded, and, on picking
 * a row, the thread and the composer with Back, Open in inbox and the customer's
 * link. It is built from the inbox page's components and shares its mutations.
 * While it is in front and the tab is visible, the list refetches every 15 s and
 * the open conversation every 5 s.
 */
export function ConversationsPanel({
  userId,
  customerId,
  canReply,
  canManage,
  onNavigate,
  connectionButton,
}: ConversationsPanelProps) {
  const { t } = useI18n("communications");
  const shown = useSidePanelShown();
  const { close, hostHasClose } = useSidePanelActions();
  const counts = useQuery(inboxCountsQueryOptions());
  const visible = usePageVisible();
  const active = shown && visible;
  const [openId, setOpenId] = useState<string | null>(null);
  const [chosen, setChosen] = useState<Filter | undefined>();
  const [autoAll, setAutoAll] = useState(false);
  const [query, setQuery] = useState("");

  const choice = chosen === "customer" && customerId === undefined ? undefined : chosen;
  const filter: Filter = choice ?? (autoAll || userId === undefined ? "all" : "mine");
  const list = useQuery(
    conversationsQueryOptions(filtersFor(filter, userId, customerId), {
      refetchInterval: active ? LIST_POLL_MS : false,
    }),
  );
  useRefetchOnResume(active, () => void list.refetch());
  // Something is assigned to the person after all (the badge's count): the default goes back to Assigned to me.
  if (autoAll && choice === undefined && (counts.data?.assignedToMeUnread ?? 0) > 0) setAutoAll(false);
  // The default is Assigned to me, or All open when nothing is assigned to the person (state set while rendering, from the list).
  if (
    choice === undefined &&
    !autoAll &&
    filter === "mine" &&
    list.isSuccess &&
    !list.isPlaceholderData &&
    list.data.pagination.totalCount === 0
  ) {
    setAutoAll(true);
  }

  const shownData: Page<ConversationListItem> | undefined = list.data && {
    ...list.data,
    data: list.data.data.filter((item) => matchesSearch(item, query)),
    pagination: {
      ...list.data.pagination,
      totalCount: query.trim()
        ? list.data.data.filter((item) => matchesSearch(item, query)).length
        : list.data.pagination.totalCount,
    },
  };

  const filters: { value: Filter; label: string }[] = [
    { value: "mine", label: t("filterMine") },
    { value: "unread", label: t("unread") },
    { value: "all", label: t("filterAllOpen") },
    ...(customerId !== undefined ? [{ value: "customer" as const, label: t("filterThisCustomer") }] : []),
  ];

  return (
    <Box
      className="conversations-panel"
      data-testid="conversations-panel"
      h="100%"
      style={{ display: "flex", flexDirection: "column", minHeight: 0 }}
    >
      {openId ? (
        <PanelConversation
          key={openId}
          id={openId}
          active={active}
          canReply={canReply}
          canManage={canManage}
          onBack={() => setOpenId(null)}
          onNavigate={onNavigate}
          connectionButton={connectionButton}
        />
      ) : (
        <>
          <Group
            justify="space-between"
            wrap="nowrap"
            px="sm"
            py={8}
            style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
          >
            <Text fw={600} size="sm">
              {t("panelConversations")}
            </Text>
            {!hostHasClose && (
              <Tooltip label={t("closePanel")}>
                <ActionIcon variant="subtle" color="gray" aria-label={t("closePanel")} onClick={close}>
                  <IconX size={18} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
          <Stack gap="xs" p="sm">
            <Chip.Group multiple={false} value={filter} onChange={(value) => setChosen(value as Filter)}>
              <Group gap={6} role="radiogroup" aria-label={t("panelFilter")}>
                {filters.map((item) => (
                  <Chip key={item.value} value={item.value} size="xs">
                    {item.label}
                  </Chip>
                ))}
              </Group>
            </Chip.Group>
            <TextInput
              size="xs"
              type="search"
              placeholder={t("searchConversations")}
              aria-label={t("searchLoadedConversations")}
              value={query}
              onChange={(event) => setQuery(event.currentTarget.value)}
            />
          </Stack>
          <Box style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
            <ConversationList
              data={shownData}
              isPending={list.isPending}
              isError={list.isError && !list.data}
              error={list.error}
              selectedId=""
              onSelect={setOpenId}
              onRefresh={() => void list.refetch()}
            />
          </Box>
        </>
      )}
    </Box>
  );
}

/** One conversation in the panel: its thread and composer under Back, Open in inbox and the customer's link. */
function PanelConversation({
  id,
  active,
  canReply,
  canManage,
  onBack,
  onNavigate,
  connectionButton,
}: {
  id: string;
  active: boolean;
  canReply: boolean;
  canManage: boolean;
  onBack: () => void;
  onNavigate: (route: string) => void;
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
}) {
  const { t } = useI18n("communications");
  const { close, hostHasClose } = useSidePanelActions();
  const detail = useQuery(conversationQueryOptions(id, { refetchInterval: active ? CONVERSATION_POLL_MS : false }));
  useRefetchOnResume(active, () => void detail.refetch());
  const tags = useQuery(tagsQueryOptions());
  const { invalidate, markRead, workflow, tagMutation, suggest } = useConversationActions(id);
  const conversation = detail.data;

  // Looking at a conversation reads it, so the badge clears; new activity while it is in front reads again.
  const marked = useRef("");
  const { mutate: markReadNow } = markRead;
  useEffect(() => {
    if (!active || !conversation) return;
    const unread =
      !conversation.lastReadAt || new Date(conversation.lastReadAt) < new Date(conversation.lastActivityAt);
    const key = `${conversation.id}|${conversation.lastActivityAt}`;
    if (!unread || marked.current === key) return;
    marked.current = key;
    markReadNow();
  }, [active, conversation, markReadNow]);

  const go = (route: string) => (event: { preventDefault: () => void; metaKey: boolean; ctrlKey: boolean }) => {
    if (event.metaKey || event.ctrlKey) return;
    event.preventDefault();
    onNavigate(route);
  };
  const inboxRoute = `/communications/inbox?conversationId=${encodeURIComponent(id)}`;

  return (
    <>
      <Group
        justify="space-between"
        wrap="nowrap"
        gap="xs"
        px="xs"
        py={6}
        style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
      >
        <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
          <Tooltip label={t("back")}>
            <ActionIcon variant="subtle" color="gray" aria-label={t("back")} onClick={onBack}>
              <IconArrowLeft size={18} />
            </ActionIcon>
          </Tooltip>
          {conversation?.customerId ? (
            <Anchor
              size="sm"
              href={`/customers/${conversation.customerId}`}
              onClick={go(`/customers/${conversation.customerId}`)}
              lineClamp={1}
            >
              {t("openCustomer", { id: conversation.customerId })}
            </Anchor>
          ) : null}
        </Group>
        <Group gap={2} wrap="nowrap">
          <Tooltip label={t("openInInbox")}>
            <Anchor
              href={inboxRoute}
              onClick={go(inboxRoute)}
              aria-label={t("openInInbox")}
              style={{ display: "inline-flex" }}
            >
              <ActionIcon component="span" variant="subtle" color="gray">
                <IconExternalLink size={18} />
              </ActionIcon>
            </Anchor>
          </Tooltip>
          {!hostHasClose && (
            <Tooltip label={t("closePanel")}>
              <ActionIcon variant="subtle" color="gray" aria-label={t("closePanel")} onClick={close}>
                <IconX size={18} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </Group>
      <Box className="panel-thread" style={{ flex: 1, minHeight: 0 }}>
        {detail.isPending ? (
          <ContentSkeleton rows={5} p="md" />
        ) : detail.isError && !conversation ? (
          <Alert m="md" color="red">
            {t("couldNotLoadConversation")}
          </Alert>
        ) : (
          conversation && (
            <>
              {detail.isError && (
                <Alert color="yellow" py={4} px="sm" role="status">
                  {t("refreshFailed")}
                </Alert>
              )}
              <ConversationThread
                conversation={conversation}
                tags={tags.data || []}
                onRead={() => markRead.mutate()}
                onWorkflow={(body) => workflow.mutate(body)}
                onTag={(tagId, remove) => tagMutation.mutate({ tagId, remove })}
                onSent={invalidate}
                onSuggest={() => suggest.mutate()}
                suggestionPending={suggest.isPending}
                suggestionError={suggest.error}
                connectionButton={connectionButton}
                canReply={canReply}
                canManage={canManage}
              />
            </>
          )
        )}
      </Box>
    </>
  );
}
