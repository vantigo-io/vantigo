import { ActionIcon, Badge, Indicator, Tooltip } from "@mantine/core";
import { IconMessages } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { type SidePanel, type SidePanelController, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useCallback } from "react";
import { type InboxCounts, inboxCountsQueryOptions } from "../api/conversations";
import type { ConnectionButtonProps } from "../components/ConversationComposer";
import { ConversationsPanel } from "../components/ConversationsPanel";
import { COUNTS_POLL_MS, usePollInterval } from "../lib/polling";
import { conversationsShortcutHint, useConversationsShortcut } from "../lib/shortcut";
import "../i18n";

export const CONVERSATIONS_PANEL_KEY = "conversations";

/** What the button's badge shows: the unread conversations assigned to the person, else a dot for any unread, else nothing. */
export const badgeFor = (counts: InboxCounts | undefined): { label?: number; dot: boolean } =>
  counts && counts.assignedToMeUnread > 0
    ? { label: counts.assignedToMeUnread, dot: true }
    : counts && counts.unread > 0
      ? { dot: true }
      : { dot: false };

export type UseConversationsShellOptions = {
  /** The shell's shared side panel; Conversations is one entry in it (Messenger design N24). */
  side: SidePanelController;
  /** The host's gate: signed in, not held at MFA enrolment, the module on, and communications:conversations-view. */
  enabled: boolean;
  /** The inbox page is on screen: it is the same view, so the panel, its button and its shortcut stand down. */
  onInboxPage?: boolean;
  userId?: string;
  /** The customer the main page shows. */
  customerId?: number;
  canReply: boolean;
  canManage: boolean;
  onNavigate: (route: string) => void;
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
};

/**
 * Everything the shell needs of Conversations, in one hook, like the Assistant's:
 * the header button with its badge (the counts are read every 30 s while the tab
 * is visible, and on focus), the panel's entry for the side panel, and the
 * Cmd/Ctrl+Shift+U shortcut.
 */
export const useConversationsShell = ({
  side,
  enabled,
  onInboxPage = false,
  userId,
  customerId,
  canReply,
  canManage,
  onNavigate,
  connectionButton,
}: UseConversationsShellOptions) => {
  const { t } = useI18n("communications");
  const visible = enabled && !onInboxPage;
  const poll = usePollInterval(COUNTS_POLL_MS, visible);
  const counts = useQuery(inboxCountsQueryOptions({ enabled: visible, refetchInterval: poll })).data;
  const open = side.isShown(CONVERSATIONS_PANEL_KEY);
  const { toggle } = side;
  const toggleConversations = useCallback(() => toggle(CONVERSATIONS_PANEL_KEY), [toggle]);
  useConversationsShortcut(toggleConversations, visible);

  const badge = badgeFor(counts);
  const countsLabel =
    counts && counts.assignedToMeUnread > 0
      ? t("badgeAssigned", { count: counts.assignedToMeUnread })
      : counts && counts.unread > 0
        ? t("badgeUnread", { count: counts.unread })
        : undefined;
  const label = open ? t("closeConversationsPanel") : t("openConversationsPanel");

  return {
    /** The panel can be used from where the person is. */
    visible,
    /** For `headerActions`; null when the person cannot use the panel here. */
    button: visible ? (
      <Tooltip label={`${label} (${conversationsShortcutHint()})`}>
        <Indicator
          disabled={!badge.dot}
          size={badge.label !== undefined ? 16 : 10}
          offset={4}
          color="red"
          label={badge.label !== undefined ? (badge.label > 99 ? "99+" : badge.label) : undefined}
        >
          <ActionIcon
            variant={open ? "light" : "subtle"}
            color="gray"
            size="lg"
            aria-label={countsLabel ? `${label}. ${countsLabel}` : label}
            aria-pressed={open}
            onClick={toggleConversations}
          >
            <IconMessages size={20} stroke={1.5} />
          </ActionIcon>
        </Indicator>
      </Tooltip>
    ) : null,
    /** The Conversations entry for the side panel; undefined when the person cannot use it. Hidden in the inbox. */
    sidePanel: enabled
      ? ({
          key: CONVERSATIONS_PANEL_KEY,
          label: t("panelConversations"),
          icon: <IconMessages size={16} stroke={1.5} />,
          badge:
            badge.label !== undefined ? (
              <Badge size="xs" color="red" circle>
                {badge.label > 99 ? "99+" : badge.label}
              </Badge>
            ) : undefined,
          hidden: onInboxPage,
          render: () => (
            <ConversationsPanel
              userId={userId}
              customerId={customerId}
              canReply={canReply}
              canManage={canManage}
              onNavigate={onNavigate}
              connectionButton={connectionButton}
            />
          ),
        } satisfies SidePanel)
      : undefined,
  };
};
