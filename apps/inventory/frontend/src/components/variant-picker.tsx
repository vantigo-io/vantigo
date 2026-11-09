import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type InventoryVariantRef, variantSearchQueryOptions } from "../api/stock";
import { useInventoryFormat, variantLabel } from "../lib/format";

const SEARCH_DEBOUNCE_MS = 250;

export interface VariantPickerProps {
  label: string;
  value: InventoryVariantRef | undefined;
  onChange: (variant: InventoryVariantRef | undefined) => void;
  error?: string;
  /** Shown without a label, as in a line editor's row. */
  compact?: boolean;
  /** A site, so each variant found carries its primary location there (I55). */
  siteId?: number;
}

/**
 * A goods variant searched by SKU, product name or barcode
 * (`GET /inventory/variants?q`, I22) — Inventory's own search, so a warehouse
 * worker needs none of Products' keys. Nothing is listed until something is
 * typed.
 */
export const VariantPicker = ({ label, value, onChange, error, compact, siteId }: VariantPickerProps) => {
  const { t } = useInventoryFormat();
  const [search, setSearch] = useState("");
  // Picking an option writes its label into the search box; that is not a search.
  const term = value && search === variantLabel(value) ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({
    ...variantSearchQueryOptions(debounced, undefined, siteId),
    enabled: debounced !== "",
  });
  const found = debounced === "" ? [] : (data ?? []);
  const options = found.map((variant) => ({ value: String(variant.id), label: variantLabel(variant) }));
  if (value && !options.some((option) => option.value === String(value.id)))
    options.unshift({ value: String(value.id), label: variantLabel(value) });

  return (
    <Select
      label={compact ? undefined : label}
      aria-label={label}
      placeholder={t("typeToSearchVariants")}
      searchable
      value={value ? String(value.id) : null}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      // The server has already matched the words.
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noVariantsFound")}
      error={error}
      onChange={(next) => {
        if (next === null) return onChange(undefined);
        onChange(found.find((variant) => String(variant.id) === next) ?? value);
      }}
    />
  );
};
