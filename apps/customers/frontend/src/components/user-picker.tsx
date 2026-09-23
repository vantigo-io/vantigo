import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignableUsersQueryOptions } from "../api/owner";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** A user this picker can show: the two fields any of its callers has. */
export interface PickableUser {
  userId: string;
  displayName: string;
}

/**
 * A searchable `Select` over the directory `GET /customers/assignable-users`
 * answers, for any field of this module that names a colleague: the customer's
 * owner (owner and tags design D1, D3) and a timeline follow-up's assignee
 * (follow-ups design D4). It was `OwnerPicker` until the second caller arrived;
 * the only thing that was ever owner-specific about it is the words, so the
 * words are props and everything else is unchanged.
 *
 * Two rules that look like details and are not:
 *
 *  - `selected` is kept on the option list whatever the search returns. The API
 *    answers ACTIVE users only, and both designs rule that somebody disabled
 *    after being chosen keeps what they were given — so without this the picker
 *    would blank the very name it is meant to be showing.
 *  - Mantine's own `filter` is replaced with one that keeps every option. The
 *    list is already the answer to the search (narrowed by the API, on the
 *    user's email too, which their display name need not contain), so filtering
 *    it again client-side would drop rows the server deliberately returned.
 */
export const UserPicker = ({
  label,
  placeholder,
  value,
  selected,
  onChange,
  disabled,
  clearLabel,
}: {
  label: string;
  placeholder: string;
  value: string | null;
  /** The user already chosen, so a name survives a search that does not contain it. */
  selected?: PickableUser | null;
  onChange: (value: string | null) => void;
  disabled?: boolean;
  clearLabel: string;
}) => {
  const { t } = useI18n("customers");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data: found } = useQuery(assignableUsersQueryOptions(debouncedSearch));

  const options = new Map<string, string>();
  for (const user of found ?? []) options.set(user.userId, user.displayName);
  if (value && !options.has(value) && selected) options.set(value, selected.displayName);

  return (
    <Select
      label={label}
      placeholder={placeholder}
      searchable
      clearable
      disabled={disabled}
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noAssignableUsers")}
      data={[...options].map(([userId, displayName]) => ({ value: userId, label: displayName }))}
      value={value}
      onChange={onChange}
      // Mantine's clear button keeps its default `aria-hidden`: it is not a
      // control a screen-reader user needs, since clearing the Select from the
      // keyboard is what the empty option is for. The label is still there, so
      // a pointer test can find it.
      clearButtonProps={{ "aria-label": clearLabel }}
    />
  );
};
