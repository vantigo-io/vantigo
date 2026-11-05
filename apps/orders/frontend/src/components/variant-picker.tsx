import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type VariantOption, variantSearchQueryOptions } from "../api/products";
import { useOrdersFormat } from "../lib/format";

export interface VariantPickerProps {
  /** The row's variant, as the row names it. */
  value: { variantId: number; sku: string | null; name: string } | null;
  onChange: (variant: VariantOption | null) => void;
  label: string;
  disabled?: boolean;
  error?: string;
}

const optionLabel = (v: { sku: string | null; name: string }) => (v.sku ? `${v.sku} · ${v.name}` : v.name);

/**
 * A product variant searched by SKU or product name in the products module's
 * own list (`products:products-view`). Nothing is listed until something is
 * typed; the server prices the line once it is saved (O16).
 */
export const VariantPicker = ({ value, onChange, label, disabled, error }: VariantPickerProps) => {
  const { t } = useOrdersFormat();
  const [search, setSearch] = useState("");
  const term = value && search === optionLabel(value) ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, 250);
  const { data, isFetching } = useQuery({ ...variantSearchQueryOptions(debounced), enabled: debounced !== "" });
  const found = debounced === "" ? [] : (data ?? []);
  const options = found.map((v) => ({ value: String(v.variantId), label: optionLabel(v) }));
  if (value && !options.some((o) => o.value === String(value.variantId)))
    options.unshift({ value: String(value.variantId), label: optionLabel(value) });
  return (
    <Select
      aria-label={label}
      placeholder={t("searchVariants")}
      searchable
      clearable
      disabled={disabled}
      value={value ? String(value.variantId) : null}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noVariantsFound")}
      error={error}
      onChange={(next) => {
        if (next === null) return onChange(null);
        const picked = found.find((v) => String(v.variantId) === next);
        if (picked) onChange(picked);
      }}
      size="xs"
    />
  );
};
