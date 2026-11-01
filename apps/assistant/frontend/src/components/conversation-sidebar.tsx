import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Center,
  Group,
  Loader,
  Menu,
  Modal,
  NavLink,
  ScrollArea,
  Stack,
  Switch,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconArchive,
  IconArchiveOff,
  IconDotsVertical,
  IconPencil,
  IconPin,
  IconPinnedOff,
  IconPlus,
  IconSearch,
  IconTrash,
} from "@tabler/icons-react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  conversationsQueryKey,
  conversationsQueryOptions,
  deleteConversation,
  updateConversation,
} from "../api/assistant";
import type { AssistantConversation } from "../api/types";
import { type ConversationGroupKey, groupConversations } from "../lib/groups";

export interface ConversationSidebarProps {
  /** The open conversation, highlighted. */
  selectedId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  /** A conversation was deleted; the app leaves it when it was the open one. */
  onDeleted?: (id: string) => void;
  /** The name of an app by its key, for the origin badge. */
  appLabel?: (app: string) => string;
}

const groupLabel: Record<ConversationGroupKey, string> = {
  pinned: "groupPinned",
  today: "groupToday",
  yesterday: "groupYesterday",
  week: "groupWeek",
  older: "groupOlder",
};

const fallbackAppLabel = (app: string) => (app ? app.charAt(0).toUpperCase() + app.slice(1) : app);

/**
 * Every conversation the person has had, from anywhere in Vantigo: searchable,
 * pinned first and then grouped by when they were last active, each with where
 * it began. The row's menu renames, pins, archives and deletes; opening a row
 * resumes it. Archived conversations are behind a switch.
 */
export const ConversationSidebar = ({ selectedId, onSelect, onNew, onDeleted, appLabel }: ConversationSidebarProps) => {
  const { t } = useI18n("assistant");
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [q] = useDebouncedValue(search.trim(), 250);
  const [archived, setArchived] = useState(false);
  const [renaming, setRenaming] = useState<AssistantConversation | undefined>();
  const [title, setTitle] = useState("");

  const list = useInfiniteQuery(conversationsQueryOptions({ q: q || undefined, archived }));
  const conversations = (list.data?.pages ?? []).flatMap((page) => page.items);
  const groups = groupConversations(conversations);

  const refresh = () => void queryClient.invalidateQueries({ queryKey: conversationsQueryKey });
  const failed = () => notifications.show({ color: "red", message: t("requestFailed") });

  const patch = useMutation({
    mutationFn: ({ id, ...changes }: { id: string; title?: string; pinned?: boolean; archived?: boolean }) =>
      updateConversation(id, changes),
    onSuccess: (saved) => {
      void queryClient.invalidateQueries({ queryKey: [...conversationsQueryKey] });
      void queryClient.invalidateQueries({ queryKey: ["assistant", "conversation", saved.id] });
    },
    onError: failed,
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteConversation(id),
    onSuccess: (_, id) => {
      refresh();
      queryClient.removeQueries({ queryKey: ["assistant", "conversation", id] });
      onDeleted?.(id);
      notifications.show({ color: "teal", message: t("deleted") });
    },
    onError: failed,
  });

  const confirmDelete = (conversation: AssistantConversation) =>
    modals.openConfirmModal({
      title: t("deleteTitle"),
      children: <Text size="sm">{t("deleteBody")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(conversation.id),
    });

  const startRename = (conversation: AssistantConversation) => {
    setRenaming(conversation);
    setTitle(conversation.title);
  };

  const row = (conversation: AssistantConversation) => (
    <NavLink
      key={conversation.id}
      component="div"
      active={conversation.id === selectedId}
      onClick={() => onSelect(conversation.id)}
      label={
        <Text size="sm" lineClamp={1}>
          {conversation.title || t("untitled")}
        </Text>
      }
      description={
        <Group gap={6} wrap="nowrap">
          <Badge size="xs" variant="light" color="gray" data-testid="origin-badge">
            {t("startedIn", { app: (appLabel ?? fallbackAppLabel)(conversation.originApp) })}
          </Badge>
          {conversation.running && <Loader size={10} />}
        </Group>
      }
      rightSection={
        <Menu position="bottom-end" withinPortal>
          <Menu.Target>
            <ActionIcon
              variant="subtle"
              size="sm"
              color="gray"
              aria-label={t("conversationActions", { title: conversation.title || t("untitled") })}
              onClick={(event) => event.stopPropagation()}
            >
              <IconDotsVertical size={14} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown onClick={(event) => event.stopPropagation()}>
            <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => startRename(conversation)}>
              {t("rename")}
            </Menu.Item>
            <Menu.Item
              leftSection={conversation.pinned ? <IconPinnedOff size={14} /> : <IconPin size={14} />}
              onClick={() => patch.mutate({ id: conversation.id, pinned: !conversation.pinned })}
            >
              {conversation.pinned ? t("unpin") : t("pin")}
            </Menu.Item>
            <Menu.Item
              leftSection={conversation.archived ? <IconArchiveOff size={14} /> : <IconArchive size={14} />}
              onClick={() => patch.mutate({ id: conversation.id, archived: !conversation.archived })}
            >
              {conversation.archived ? t("unarchive") : t("archive")}
            </Menu.Item>
            <Menu.Divider />
            <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => confirmDelete(conversation)}>
              {t("delete")}
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      }
    />
  );

  return (
    <Stack gap="xs" h="100%" style={{ minHeight: 0 }}>
      <Button leftSection={<IconPlus size={16} />} variant="light" onClick={onNew}>
        {t("newConversation")}
      </Button>
      <TextInput
        aria-label={t("searchConversations")}
        placeholder={t("searchConversations")}
        leftSection={<IconSearch size={14} />}
        value={search}
        onChange={(event) => setSearch(event.currentTarget.value)}
      />
      <Switch
        size="xs"
        label={t("showArchived")}
        checked={archived}
        onChange={(event) => setArchived(event.currentTarget.checked)}
      />
      <ScrollArea flex={1} type="auto" style={{ minHeight: 0 }}>
        {list.isPending && (
          <Center p="md">
            <Loader size="sm" />
          </Center>
        )}
        {list.isError && (
          <Text size="sm" c="red" p="xs">
            {t("requestFailed")}
          </Text>
        )}
        {list.isSuccess && groups.length === 0 && (
          <Text size="sm" c="dimmed" p="xs">
            {q ? t("noMatches") : t("noConversations")}
          </Text>
        )}
        {groups.map((group) => (
          <Box key={group.key} mb="xs">
            <Text size="xs" fw={700} tt="uppercase" c="dimmed" px="xs" py={4}>
              {t(groupLabel[group.key])}
            </Text>
            {group.conversations.map(row)}
          </Box>
        ))}
        {list.hasNextPage && (
          <Button
            variant="subtle"
            size="xs"
            fullWidth
            loading={list.isFetchingNextPage}
            onClick={() => void list.fetchNextPage()}
          >
            {t("loadMore")}
          </Button>
        )}
      </ScrollArea>
      <Modal opened={renaming !== undefined} onClose={() => setRenaming(undefined)} title={t("renameTitle")} centered>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const next = title.trim();
            if (renaming && next) patch.mutate({ id: renaming.id, title: next });
            setRenaming(undefined);
          }}
        >
          <Stack>
            <TextInput
              label={t("titleLabel")}
              value={title}
              maxLength={200}
              data-autofocus
              onChange={(event) => setTitle(event.currentTarget.value)}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setRenaming(undefined)}>
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={title.trim() === ""}>
                {t("save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
