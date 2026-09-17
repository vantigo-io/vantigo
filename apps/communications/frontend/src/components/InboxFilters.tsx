import { Chip, Group, SegmentedControl, Select } from "@mantine/core";
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

const ALL = "all";
const STATUSES: readonly ConversationStatus[] = ["open", "closed", "archived"];

/**
 * The inbox's filters, laid out for the toolbar: a status segmented control,
 * an unread chip and a tag select. Every change lands in the URL and drops
 * the selected conversation, since it may no longer be in the list.
 */
export function InboxFilters({ search, tags, onSearchChange }: InboxFiltersProps) {
  const { t } = useI18n("communications");
  const change = (next: Partial<InboxSearch>) => onSearchChange({ ...search, ...next, conversationId: undefined });

  return (
    <Group gap="sm" wrap="wrap">
      <SegmentedControl
        size="xs"
        value={search.status ?? ALL}
        onChange={(value) => change({ status: value === ALL ? undefined : (value as ConversationStatus) })}
        data={[
          { value: ALL, label: t("statusAll") },
          ...STATUSES.map((status) => ({ value: status, label: t(`status.${status}`) })),
        ]}
      />
      <Chip
        size="sm"
        checked={search.unreadOnly === true}
        onChange={(checked) => change({ unreadOnly: checked ? true : undefined })}
      >
        {t("unread")}
      </Chip>
      <Select
        size="xs"
        w={180}
        aria-label={t("tag")}
        placeholder={t("allTags")}
        clearable
        data={tags.map((tag) => ({ value: tag.id, label: tag.name }))}
        value={search.tagId ?? null}
        onChange={(value) => change({ tagId: value ?? undefined })}
      />
    </Group>
  );
}
