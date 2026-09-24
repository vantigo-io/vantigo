import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type CustomerResponse, customersQueryOptions } from "../api/customers";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** How many customers one search offers: the projects picker's twenty. */
const CUSTOMER_PICKER_PAGE_SIZE = 20;

/**
 * Picks another customer, searching this module's own list as the user types
 * (customers merge design D4). It is the projects package's `CustomerPicker`
 * shape, copied rather than imported — module frontends never import one
 * another (docs/module-boundaries.md rule 7) — with three differences the merge
 * needs:
 *
 *  - The value is the whole customer, not an id: the modal says what will
 *    happen to it, and checks its type, before anything is sent.
 *  - The search reaches archived customers (`includeArchived`): an archived
 *    duplicate is the usual thing to absorb.
 *  - `excludeId` (the customer being merged into) and every customer already
 *    merged away are left out — the server refuses both. They are dropped here,
 *    after the search, so a page of twenty can show nineteen.
 *
 * The chosen customer stays on the option list whatever the next search
 * returns, so its name never turns into a bare id; and Mantine's own filter is
 * replaced by one that keeps every option — the API has already searched, on
 * the customer number and more besides the name the label shows.
 *
 * Only typing searches. Once a customer is picked, Mantine puts its label in
 * the input and reports that as a search too; searching for
 * "#5 Acme Norge AS · Archived" finds nothing, and reopening the list to change
 * the pick would then offer the pick alone. So the selected label reads as no
 * search at all, and the list stays the one the pick was made from.
 */
export const CustomerPicker = ({
  label,
  placeholder,
  value,
  onChange,
  excludeId,
  disabled,
}: {
  label: string;
  placeholder: string;
  value: CustomerResponse | null;
  onChange: (customer: CustomerResponse | null) => void;
  excludeId: number;
  disabled?: boolean;
}) => {
  const { t } = useI18n("customers");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data } = useQuery(
    customersQueryOptions({
      page: 1,
      pageSize: CUSTOMER_PICKER_PAGE_SIZE,
      search: debouncedSearch.trim() || undefined,
      includeArchived: true,
    }),
  );

  const optionLabel = (customer: CustomerResponse) =>
    customer.status === "archived"
      ? `#${customer.customerNumber} ${customer.name} · ${t("statusArchived")}`
      : `#${customer.customerNumber} ${customer.name}`;
  const offered = new Map<number, CustomerResponse>();
  for (const customer of data?.data ?? []) {
    if (customer.id !== excludeId && customer.mergedInto === null) offered.set(customer.id, customer);
  }
  if (value) offered.set(value.id, value);

  return (
    <Select
      label={label}
      placeholder={placeholder}
      disabled={disabled}
      searchable
      clearable
      filter={({ options }) => options}
      onSearchChange={(text) => setSearch(value && text === optionLabel(value) ? "" : text)}
      nothingFoundMessage={t("noCustomersFound")}
      data={[...offered.values()].map((customer) => ({ value: String(customer.id), label: optionLabel(customer) }))}
      value={value ? String(value.id) : null}
      onChange={(next) => onChange(next === null ? null : (offered.get(Number(next)) ?? null))}
    />
  );
};
