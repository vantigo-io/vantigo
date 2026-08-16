import { Button, Divider, Text } from "@mantine/core";
import { IconTag } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConversationStatus, Tag } from "../api/conversations";

export type InboxSearch = {
  conversationId?: string;
  status?: ConversationStatus;
  customerId?: number;
  unreadOnly?: boolean;
  tagId?: string;
};

export type InboxFiltersProps = {
  search: InboxSearch;
  tags: Tag[];
  onSearchChange: (search: InboxSearch) => void;
};

export function InboxFilters({ search, tags, onSearchChange }: InboxFiltersProps) {
  const { t } = useI18n("communications");

  return (
    <aside className="inbox-filters">
      <Text size="xs" tt="uppercase" fw={700} c="dimmed">
        {t("views")}
      </Text>
      {([undefined, "open", "closed", "archived"] as const).map((status) => (
        <Button
          key={status || "all"}
          variant={search.status === status ? "light" : "subtle"}
          justify="start"
          fullWidth
          onClick={() => onSearchChange({ ...search, status, conversationId: undefined })}
        >
          {status ? status[0].toUpperCase() + status.slice(1) : t("allConversations")}
        </Button>
      ))}
      <Divider my="sm" />
      <Button
        variant={search.unreadOnly ? "light" : "subtle"}
        justify="start"
        fullWidth
        onClick={() =>
          onSearchChange({
            ...search,
            unreadOnly: search.unreadOnly ? undefined : true,
            conversationId: undefined,
          })
        }
      >
        {t("unread")}
      </Button>
      <Text size="xs" tt="uppercase" fw={700} c="dimmed" mt="md">
        {t("tags")}
      </Text>
      {tags.map((tag) => (
        <Button
          key={tag.id}
          variant={search.tagId === tag.id ? "light" : "subtle"}
          justify="start"
          fullWidth
          leftSection={<IconTag size={14} />}
          onClick={() =>
            onSearchChange({
              ...search,
              tagId: search.tagId === tag.id ? undefined : tag.id,
              conversationId: undefined,
            })
          }
        >
          {tag.name}
        </Button>
      ))}
    </aside>
  );
}
