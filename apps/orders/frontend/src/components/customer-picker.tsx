import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type CustomerOption, customerSearchQueryOptions } from "../api/customers";
import { useOrdersFormat } from "../lib/format";

export interface CustomerPickerProps {
  value: number | null;
  onChange: (customerId: number | null) => void;
  /** Hears the picked customer as the list answered it, its name included. */
  onPicked?: (customer: CustomerOption | null) => void;
  /** The document's customer, so its name shows before any search. */
  selected?: { id: number; name?: string };
  label?: string;
  required?: boolean;
  disabled?: boolean;
  clearable?: boolean;
  /** Offer every customer, archived ones too: the lists' filter. */
  anyStatus?: boolean;
  error?: string;
}

/**
 * The customer picker: the customers module's own list, searched as the
 * person types (`customers:view`; the page decides who is offered it). The
 * picked customer's label reads as no search, so it keeps its own label
 * whatever the next search returns — as Invoices' buyer picker does.
 */
export const CustomerPicker = ({
  value,
  onChange,
  onPicked,
  selected,
  label,
  required,
  disabled,
  clearable = true,
  anyStatus,
  error,
}: CustomerPickerProps) => {
  const { t } = useOrdersFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const [picked, setPicked] = useState<CustomerOption | null>(null);
  const customers = useQuery({ ...customerSearchQueryOptions(debounced, anyStatus), enabled: !disabled });
  const optionLabel = (c: CustomerOption) => t("customerOption", { name: c.name, number: c.customerNumber });
  const data = (customers.data ?? []).map((c) => ({ value: String(c.id), label: optionLabel(c) }));
  const selectedLabel =
    value === null
      ? undefined
      : (data.find((d) => d.value === String(value))?.label ??
        (picked?.id === value ? optionLabel(picked) : selected?.id === value ? selected.name : undefined));
  if (value !== null && selectedLabel && !data.some((d) => d.value === String(value)))
    data.unshift({ value: String(value), label: selectedLabel });
  return (
    <Select
      label={label ?? t("customer")}
      placeholder={t("searchCustomers")}
      searchable
      clearable={clearable}
      required={required}
      disabled={disabled}
      data={data}
      value={value === null ? null : String(value)}
      onSearchChange={(text) => setSearch(selectedLabel !== undefined && text === selectedLabel ? "" : text)}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        const found = customers.data?.find((c) => c.id === id) ?? null;
        setPicked(found);
        onPicked?.(found);
        onChange(id);
      }}
      nothingFoundMessage={customers.isFetching ? t("searching") : t("noCustomersFound")}
      filter={({ options }) => options}
      error={error}
    />
  );
};
