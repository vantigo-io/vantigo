import { Chip, Group, SegmentedControl, Select } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConversationStatus, Tag } from "../api/conversations";
import { useAssignableOptions } from "../hooks/useAssignableOptions";

export type InboxSearch = {
  conversationId?: string;
  status?: ConversationStatus;
  customerId?: number;
  unreadOnly?: boolean;
  /** A user id, "me" or "none". */
  assignedUserId?: string;
  tagId?: string;
};

export type InboxFiltersProps = {
  search: InboxSearch;
  tags: Tag[];
  /** The names of the people the list shows, to label a person chosen by an address that is followed. */
  assigneeNames?: Record<string, string>;
  onSearchChange: (search: InboxSearch) => void;
};

const ALL = "all";
const ME = "me";
const NONE = "none";
const STATUSES: readonly ConversationStatus[] = ["open", "closed", "archived"];

/**
 * The inbox's filters, laid out for the toolbar: a status segmented control,
 * an unread chip, an assignee select (Assigned to me, Unassigned or a person)
 * and a tag select. Every change lands in the URL and drops
 * the selected conversation, since it may no longer be in the list.
 */
export function InboxFilters({ search, tags, assigneeNames = {}, onSearchChange }: InboxFiltersProps) {
  const { t } = useI18n("communications");
  const person = search.assignedUserId && search.assignedUserId !== ME && search.assignedUserId !== NONE;
  const people = useAssignableOptions({
    id: person ? (search.assignedUserId ?? null) : null,
    name: person ? assigneeNames[search.assignedUserId ?? ""] : null,
  });
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
        w={190}
        aria-label={t("filterAssignee")}
        placeholder={t("anyAssignee")}
        clearable
        searchable
        data={[{ value: ME, label: t("assignedToMe") }, { value: NONE, label: t("unassigned") }, ...people.options]}
        searchValue={people.search}
        onSearchChange={people.setSearch}
        filter={({ options }) => options}
        value={search.assignedUserId ?? null}
        onChange={(value) => change({ assignedUserId: value ?? undefined })}
      />
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
