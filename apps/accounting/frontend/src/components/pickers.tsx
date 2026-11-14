import { Select, type SelectProps } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type AccountingAccount, accountsQueryOptions, taxCodesQueryOptions } from "../api/accounting";
import { customerSearchQueryOptions, projectsQueryOptions } from "../api/lookups";
import { useAccountingFormat } from "../lib/format";

type PickerProps = Omit<SelectProps, "data" | "value" | "onChange"> & {
  value: string | null;
  onChange: (value: string | null) => void;
};

/** An account of the chart, by number and name; the inactive ones are left out unless chosen. */
export const AccountSelect = ({
  value,
  onChange,
  accountFilter,
  ...props
}: PickerProps & { accountFilter?: (account: AccountingAccount) => boolean }) => {
  const accounts = useQuery(accountsQueryOptions());
  const data = (accounts.data ?? [])
    .filter((a) => (a.active || String(a.id) === value) && (accountFilter ? accountFilter(a) : true))
    .map((a) => ({ value: String(a.id), label: `${a.number} ${a.name}` }));
  return <Select searchable clearable data={data} value={value} onChange={onChange} {...props} />;
};

/** A tax code, by code and name. */
export const TaxCodeSelect = ({ value, onChange, ...props }: PickerProps) => {
  const codes = useQuery(taxCodesQueryOptions());
  const data = (codes.data ?? [])
    .filter((c) => c.active || String(c.id) === value)
    .map((c) => ({ value: String(c.id), label: `${c.code} ${c.name}` }));
  return <Select searchable clearable data={data} value={value} onChange={onChange} {...props} />;
};

/** A customer, searched in Customers; nothing when the caller may not read customers. */
export const CustomerSelect = ({ value, onChange, ...props }: PickerProps) => {
  const { t } = useAccountingFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const customers = useQuery(customerSearchQueryOptions(debounced));
  const data = (customers.data ?? []).map((c) => ({ value: String(c.id), label: `${c.customerNumber} ${c.name}` }));
  if (value && !data.some((option) => option.value === value))
    data.unshift({ value, label: t("customerNumber", { id: value }) });
  return (
    <Select
      searchable
      clearable
      data={data}
      value={value}
      onChange={onChange}
      onSearchChange={setSearch}
      nothingFoundMessage={customers.error ? t("customersUnavailable") : t("noCustomersFound")}
      filter={({ options }) => options}
      {...props}
    />
  );
};

/** An active project, when Projects is on. */
export const ProjectSelect = ({ value, onChange, ...props }: PickerProps) => {
  const projects = useQuery(projectsQueryOptions());
  const data = (projects.data ?? []).map((p) => ({ value: String(p.id), label: `${p.code} ${p.name}` }));
  if (value && !data.some((option) => option.value === value)) data.unshift({ value, label: value });
  return <Select searchable clearable data={data} value={value} onChange={onChange} {...props} />;
};
