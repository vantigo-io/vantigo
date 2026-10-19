import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type JSX, useState } from "react";
import { calendarUsersQueryOptions } from "../api/users";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/debounce";

export interface ColleaguePickerProps {
  /** The colleague whose calendar is shown, or null for the caller's own. */
  value: string | null;
  onChange: (userId: string | null) => void;
}

/**
 * Whose calendar is shown (design D6): the caller's own, or a colleague's
 * picked from the directory's active users as the caller types.
 */
export function ColleaguePicker({ value, onChange }: ColleaguePickerProps): JSX.Element {
  const { t } = useI18n("calendar");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  // The name of the colleague picked, so it survives a search that does not contain them.
  const [picked, setPicked] = useState<{ id: string; displayName: string } | null>(null);
  const users = useQuery(calendarUsersQueryOptions(debounced));

  const options = new Map<string, string>();
  for (const user of users.data ?? []) options.set(user.id, user.displayName);
  if (value && !options.has(value)) options.set(value, picked?.id === value ? picked.displayName : t("aColleague"));

  return (
    <Select
      aria-label={t("colleagueCalendar")}
      placeholder={t("myCalendar")}
      searchable
      clearable
      w={240}
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noColleaguesFound")}
      data={[...options].map(([id, displayName]) => ({ value: id, label: displayName }))}
      value={value}
      onChange={(userId) => {
        if (userId) setPicked({ id: userId, displayName: options.get(userId) ?? "" });
        onChange(userId);
      }}
    />
  );
}
