import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignableUsersQueryOptions, type StaffAssignableUser } from "../api/people";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

export interface UserPickerProps {
  value: string | null;
  onChange: (user: StaffAssignableUser | null) => void;
  error?: string;
}

/**
 * An active user of the installation, found in the user directory through
 * `GET /assignable-users?q=` (`staff:manage`) — the self-service link's
 * picker. A user is somebody who signs in, not a person in Staff, so this is
 * a different list from {@link PersonPicker}'s. Nothing is listed before a
 * name is typed; the server answers at most twenty.
 */
export const UserPicker = ({ value, onChange, error }: UserPickerProps) => {
  const { t } = useI18n("staff");
  const [search, setSearch] = useState("");
  const [picked, setPicked] = useState<StaffAssignableUser | null>(null);
  // Picking writes the user's name into the box; that is not a new search.
  const term = picked && search === picked.displayName ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({ ...assignableUsersQueryOptions(debounced), enabled: debounced !== "" });

  const found = debounced === "" ? [] : (data ?? []);
  const options = found.map((user) => ({ value: user.id, label: user.displayName }));
  if (picked && picked.id === value && !options.some((option) => option.value === picked.id))
    options.unshift({ value: picked.id, label: picked.displayName });

  return (
    <Select
      label={t("user")}
      placeholder={t("typeToSearchUsers")}
      searchable
      clearable
      value={value}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      // The directory has already matched the name; filtering again would only drop matches.
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noUsersFound")}
      error={error}
      onChange={(id) => {
        const user = id === null ? null : (found.find((one) => one.id === id) ?? picked);
        setPicked(user);
        onChange(user);
      }}
    />
  );
};
