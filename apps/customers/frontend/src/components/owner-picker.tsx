import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { CustomerOwner } from "../api/customers";
import { assignableUsersQueryOptions } from "../api/owner";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/**
 * Who owns a customer relationship (owner and tags design D1, D3), copied from
 * projects' assignee picker (apps/projects/frontend/src/components/assignee-picker.tsx)
 * because the problem is the same one: a searchable `Select` over a directory
 * the API narrows, whose current value must survive a search that does not
 * contain it.
 *
 * Two rules that look like details and are not:
 *
 *  - `selected` is kept on the option list whatever the search returns. The API
 *    answers ACTIVE users only, and design D1 rules that an owner disabled
 *    after being assigned keeps the customer — so without this the picker would
 *    blank the very name it is meant to be showing.
 *  - Mantine's own `filter` is replaced with one that keeps every option. The
 *    list is already the answer to the search (narrowed by the API, on the
 *    user's email too, which their display name need not contain), so filtering
 *    it again client-side would drop rows the server deliberately returned.
 */
export const OwnerPicker = ({
  value,
  selected,
  onChange,
  disabled,
}: {
  value: string | null;
  /** The owner the customer already has, so a name survives a search that does not contain it. */
  selected?: CustomerOwner | null;
  onChange: (value: string | null) => void;
  disabled?: boolean;
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
      label={t("owner")}
      placeholder={t("searchOwners")}
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
      clearButtonProps={{ "aria-label": t("clearOwner") }}
    />
  );
};
