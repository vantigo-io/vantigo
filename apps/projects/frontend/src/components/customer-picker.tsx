import { type MantineSize, Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useState } from "react";
import { customerQueryOptions, customerSearchQueryOptions } from "../api/customers";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** The option standing for "no customer at all" — a project the company runs for itself. */
export type CustomerPickerValue = number | "internal" | null;

export interface CustomerPickerProps {
  value: CustomerPickerValue;
  onChange: (value: CustomerPickerValue) => void;
  /** Offers "Internal project" as the first option: the form does, the list filter does not. */
  withInternal?: boolean;
  /** The selected customer's name, when the caller already knows it and no lookup is needed. */
  selectedLabel?: string;
  label?: string;
  placeholder?: string;
  description?: ReactNode;
  error?: ReactNode;
  disabled?: boolean;
  clearable?: boolean;
  withAsterisk?: boolean;
  size?: MantineSize;
  w?: number;
}

/**
 * Picks the customer a project bills to, searching the customers API as the
 * user types. The customer the picker already holds is added to the options
 * when the current search does not contain it, so an existing choice keeps
 * its name instead of showing as a bare id.
 */
export const CustomerPicker = ({
  value,
  onChange,
  withInternal = false,
  selectedLabel,
  label,
  placeholder,
  description,
  error,
  disabled,
  clearable = true,
  withAsterisk,
  size,
  w,
}: CustomerPickerProps) => {
  const { t } = useI18n("projects");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data } = useQuery(customerSearchQueryOptions(debouncedSearch));

  const customers = data ?? [];
  const selectedId = typeof value === "number" ? value : undefined;
  const found = customers.find((customer) => customer.id === selectedId);
  const needsName = selectedId !== undefined && !found && selectedLabel === undefined;
  const { data: selected } = useQuery({ ...customerQueryOptions(selectedId ?? 0), enabled: needsName });

  const options = [
    ...(withInternal ? [{ value: "internal", label: t("internalProject") }] : []),
    ...customers.map((customer) => ({ value: String(customer.id), label: customer.name })),
  ];
  if (selectedId !== undefined && !found) {
    const name = selectedLabel ?? selected?.name;
    if (name) options.push({ value: String(selectedId), label: name });
  }

  return (
    <Select
      label={label ?? t("customer")}
      placeholder={placeholder ?? t("searchCustomers")}
      description={description}
      error={error}
      disabled={disabled}
      clearable={clearable}
      withAsterisk={withAsterisk}
      size={size}
      w={w}
      searchable
      // The customers API has already filtered; filtering again would hide
      // matches whose name does not contain the term literally.
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noCustomersFound")}
      data={options}
      value={value === null ? null : String(value)}
      onChange={(next) => onChange(next === null ? null : next === "internal" ? "internal" : Number(next))}
    />
  );
};
