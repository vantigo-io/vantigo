import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignableUsersQueryOptions, projectRolesQueryOptions } from "../api/people";
import type { TaskAssignee } from "../api/tasks";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

export interface AssigneePickerProps {
  projectId: number;
  value: string | null;
  onChange: (value: string | null) => void;
  /** The assignee the task already has, so a name survives a search that does not contain it. */
  selected?: TaskAssignee;
  label?: string;
  disabled?: boolean;
}

/**
 * Who a task is assigned to. The API accepts any active user, so the picker
 * offers the project's own people first — who a task is nearly always given to
 * — and, as the caller types, the assignable users the directory answers with.
 * An inactive account is never offered, but one already on the task keeps its
 * name rather than turning into a bare uuid.
 */
export const AssigneePicker = ({ projectId, value, onChange, selected, label, disabled }: AssigneePickerProps) => {
  const { t } = useI18n("projects");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data: people } = useQuery(projectRolesQueryOptions(projectId));
  const { data: others } = useQuery(assignableUsersQueryOptions(projectId, debouncedSearch));

  const options = new Map<string, string>();
  for (const person of people ?? []) if (person.active) options.set(person.userId, person.displayName);
  for (const person of others ?? []) options.set(person.userId, person.displayName);
  if (value && selected && !options.has(value)) options.set(value, selected.displayName);

  return (
    <Select
      label={label ?? t("assignee")}
      placeholder={t("searchAssignees")}
      searchable
      clearable
      disabled={disabled}
      // Both APIs have already filtered; filtering again would hide a project
      // member whose display name does not contain the term literally.
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noAssignableUsers")}
      data={[...options].map(([userId, displayName]) => ({ value: userId, label: displayName }))}
      value={value}
      onChange={onChange}
    />
  );
};
