import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type CustomerOption, customerSearchQueryOptions } from "../api/customers";
import "../i18n";

export interface CustomerPickerProps {
  value: number | null;
  onChange: (customerId: number | null) => void;
  /**
   * The customer a draft already has, so its name shows before any search.
   * The name is that id's only: once another customer is picked it is never
   * shown for the new one.
   */
  selected?: { id: number; name?: string };
  label?: string;
  error?: string;
  required?: boolean;
  readOnly?: boolean;
  /** Whether the pick can be cleared; a draft always has a buyer, so its editor says no. */
  clearable?: boolean;
  /** Offer every customer, archived ones too, not only the active ones: the list's filter. */
  anyStatus?: boolean;
}

/**
 * The buyer picker (D1, D12): the customers module's own list, searched as the
 * person types. Only a caller holding `customers:view` is offered it — the
 * page decides that; the picker assumes it.
 *
 * Only typing searches. Once a customer is picked, Mantine puts its label in
 * the input and reports that as a search too; the customers API finds nothing
 * for "Acme AS (10001)", and the picked option would then have no label of its
 * own. So the selected label reads as no search at all — the customers app's
 * picker does the same — and the picked customer keeps its own label whatever
 * the next search returns.
 */
export const CustomerPicker = ({
  value,
  onChange,
  selected,
  label,
  error,
  required,
  readOnly,
  clearable = true,
  anyStatus,
}: CustomerPickerProps) => {
  const { t } = useI18n("invoices");
  // What the person typed, for the query only: Mantine keeps the input's text.
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const [picked, setPicked] = useState<CustomerOption | null>(null);
  const customers = useQuery(customerSearchQueryOptions(debounced, anyStatus));
  const optionLabel = (c: CustomerOption) => t("customerOption", { name: c.name, number: c.customerNumber });
  const data = (customers.data ?? []).map((c) => ({ value: String(c.id), label: optionLabel(c) }));
  // The chosen customer's label: the one picked here, or the draft's own
  // buyer by name, or the option the current search returned.
  const selectedLabel =
    value === null
      ? undefined
      : picked?.id === value
        ? optionLabel(picked)
        : selected?.id === value && selected.name
          ? selected.name
          : data.find((d) => d.value === String(value))?.label;
  if (value !== null && selectedLabel && !data.some((d) => d.value === String(value))) {
    data.unshift({ value: String(value), label: selectedLabel });
  }
  return (
    <Select
      label={label ?? t("customer")}
      placeholder={t("searchCustomers")}
      searchable
      clearable={clearable}
      required={required}
      readOnly={readOnly}
      data={data}
      value={value === null ? null : String(value)}
      onSearchChange={(text) => setSearch(selectedLabel !== undefined && text === selectedLabel ? "" : text)}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        setPicked(customers.data?.find((c) => c.id === id) ?? null);
        onChange(id);
      }}
      nothingFoundMessage={customers.isFetching ? t("searching") : t("noCustomersFound")}
      filter={({ options }) => options}
      error={error}
    />
  );
};
