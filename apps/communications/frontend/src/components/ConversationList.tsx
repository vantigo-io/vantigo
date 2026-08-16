import { ActionIcon, Alert, Badge, Group, Loader, Text } from "@mantine/core";
import { IconCheck } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConversationListItem, Page } from "../api/conversations";
import { participantName, relative } from "./conversationHelpers";

export type ConversationListProps = {
  data: Page<ConversationListItem> | undefined;
  isPending: boolean;
  isError: boolean;
  error: Error | null;
  selectedId: string;
  onSelect: (id: string) => void;
  onRefresh: () => void;
};

export function ConversationList({
  data,
  isPending,
  isError,
  error,
  selectedId,
  onSelect,
  onRefresh,
}: ConversationListProps) {
  const { t } = useI18n("communications");

  return (
    <section className="conversation-list">
      <Group justify="space-between" px="md" py="sm">
        <Text fw={700}>{t("conversationsCount", { count: data?.pagination.totalCount ?? 0 })}</Text>
        <ActionIcon variant="subtle" aria-label={t("refresh")} onClick={onRefresh}>
          <IconCheck size={16} />
        </ActionIcon>
      </Group>
      {isPending ? (
        <Loader m="md" />
      ) : isError ? (
        <Alert m="md" color="red">
          {t("couldNotLoadConversations", { error: error?.message })}
        </Alert>
      ) : data?.data.length === 0 ? (
        <Text c="dimmed" p="md">
          {t("noConversationsMatch")}
        </Text>
      ) : (
        data?.data.map((item) => (
          <button
            type="button"
            className={`conversation-row ${item.id === selectedId ? "selected" : ""} ${item.unread ? "unread" : ""}`}
            key={item.id}
            onClick={() => onSelect(item.id)}
          >
            <Group justify="space-between" wrap="nowrap">
              <Text fw={item.unread ? 800 : 600} lineClamp={1}>
                {item.participants[0] ? participantName(item.participants[0]) : "Unknown participant"}
              </Text>
              <Text size="xs" c="dimmed">
                {relative(item.lastActivityAt)}
              </Text>
            </Group>
            <Text size="sm" fw={item.unread ? 700 : 500} lineClamp={1}>
              {item.subject || "No subject"}
            </Text>
            <Text size="xs" c="dimmed" lineClamp={1}>
              {item.previewText || "No preview"}
            </Text>
            <Group gap={4} mt={5}>
              {item.tags.map((tag) => (
                <Badge key={tag.id} size="xs" variant="light" color={tag.color || "indigo"}>
                  {tag.name}
                </Badge>
              ))}
              <Badge size="xs" variant="light" color={item.status === "open" ? "teal" : "gray"}>
                {item.status}
              </Badge>
            </Group>
          </button>
        ))
      )}
    </section>
  );
}
