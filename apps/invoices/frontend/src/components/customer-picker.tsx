import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { customerSearchQueryOptions } from "../api/customers";
import "../i18n";

export interface CustomerPickerProps {
  value: number | null;
  onChange: (customerId: number | null) => void;
  /** The chosen customer's name, when the draft already has one, so it shows before any search. */
  selectedName?: string;
  label?: string;
  error?: string;
  required?: boolean;
  readOnly?: boolean;
  /** Offer every customer, archived ones too, not only the active ones: the list's filter. */
  anyStatus?: boolean;
}

/**
 * The buyer picker (D1, D12): the customers module's own list, searched as the
 * person types. Only a caller holding `customers:view` is offered it — the
 * page decides that; the picker assumes it.
 */
export const CustomerPicker = ({
  value,
  onChange,
  selectedName,
  label,
  error,
  required,
  readOnly,
  anyStatus,
}: CustomerPickerProps) => {
  const { t } = useI18n("invoices");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const customers = useQuery(customerSearchQueryOptions(debounced, anyStatus));
  const data = (customers.data ?? []).map((c) => ({ value: String(c.id), label: `${c.name} (${c.customerNumber})` }));
  if (value !== null && selectedName && !data.some((d) => d.value === String(value))) {
    data.unshift({ value: String(value), label: selectedName });
  }
  return (
    <Select
      label={label ?? t("customer")}
      placeholder={t("searchCustomers")}
      searchable
      clearable
      required={required}
      readOnly={readOnly}
      data={data}
      value={value === null ? null : String(value)}
      searchValue={search}
      onSearchChange={setSearch}
      onChange={(next) => onChange(next === null ? null : Number(next))}
      nothingFoundMessage={customers.isFetching ? t("searching") : t("noCustomersFound")}
      filter={({ options }) => options}
      error={error}
    />
  );
};
