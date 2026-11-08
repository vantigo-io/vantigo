import { Avatar, Select } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { useAssignableOptions } from "../hooks/useAssignableOptions";

export type AssigneeSelectProps = {
  /** The assignee's user id; null when unassigned. */
  value: string | null;
  /** The assignee's display name, for the option standing for them. */
  name: string | null | undefined;
  disabled?: boolean;
  onChange: (userId: string | null) => void;
};

/**
 * The Assignee select of a conversation (design V2): a searchable list of the active users,
 * with the chosen person's avatar. Clearing it leaves the conversation unassigned.
 */
export function AssigneeSelect({ value, name, disabled, onChange }: AssigneeSelectProps) {
  const { t } = useI18n("communications");
  const { search, setSearch, options } = useAssignableOptions({ id: value, name });
  return (
    <Select
      aria-label={t("assignee")}
      placeholder={t("unassigned")}
      nothingFoundMessage={t("noPeopleFound")}
      searchable
      clearable
      clearButtonProps={{ "aria-label": t("clearAssignee") }}
      disabled={disabled}
      data={options}
      value={value}
      searchValue={search}
      onSearchChange={setSearch}
      filter={({ options: all }) => all}
      leftSection={value ? <Avatar size="xs" radius="xl" name={name || undefined} color="initials" /> : undefined}
      onChange={onChange}
    />
  );
}
