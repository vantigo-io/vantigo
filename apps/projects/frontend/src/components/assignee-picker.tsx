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

  const term = debouncedSearch.trim().toLowerCase();
  const options = new Map<string, string>();
  // The roles endpoint takes no search term, so the project's own people are
  // narrowed here. The directory has already narrowed the others — and on
  // their email too, which their display name need not contain — so the same
  // filter must not be applied to them.
  for (const person of people ?? []) {
    if (!person.active) continue;
    if (term && !person.displayName.toLowerCase().includes(term)) continue;
    options.set(person.userId, person.displayName);
  }
  for (const person of others ?? []) options.set(person.userId, person.displayName);
  // Whoever is assigned stays on the list whatever the search narrows to,
  // otherwise the picker would blank the name it is meant to be showing.
  if (value && !options.has(value)) {
    const assigned = selected?.displayName ?? (people ?? []).find((person) => person.userId === value)?.displayName;
    if (assigned) options.set(value, assigned);
  }

  return (
    <Select
      label={label ?? t("assignee")}
      placeholder={t("searchAssignees")}
      searchable
      clearable
      disabled={disabled}
      // The list above is already the answer to the search: the project's own
      // people narrowed here, the directory's narrowed by the API — on their
      // email too, which their display name need not contain — and whoever is
      // assigned kept on it regardless. Mantine's own filter would drop the
      // last two, so it is replaced by one that keeps every option.
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noAssignableUsers")}
      data={[...options].map(([userId, displayName]) => ({ value: userId, label: displayName }))}
      value={value}
      onChange={onChange}
    />
  );
};
