import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type ManufacturingVariantRef, variantSearchQueryOptions } from "../api/variants";
import { useManufacturingFormat, variantLabel } from "../lib/format";

const SEARCH_DEBOUNCE_MS = 250;

export interface VariantPickerProps {
  label: string;
  value: ManufacturingVariantRef | undefined;
  onChange: (variant: ManufacturingVariantRef | undefined) => void;
  error?: string;
  /** Shown without a label, as in a line's row. */
  compact?: boolean;
  disabled?: boolean;
  withAsterisk?: boolean;
  /** Offers only variants with an active BoM, as a new manufacturing order needs (bom_required). */
  onlyWithBom?: boolean;
}

/**
 * A goods variant searched by SKU or product name
 * (`GET /manufacturing/variants?q`) — Manufacturing's own search, as
 * Inventory's picker uses Inventory's, so a planner needs none of Products'
 * keys. Nothing is listed until something is typed.
 */
export const VariantPicker = ({
  label,
  value,
  onChange,
  error,
  compact,
  disabled,
  withAsterisk,
  onlyWithBom,
}: VariantPickerProps) => {
  const { t } = useManufacturingFormat();
  const [search, setSearch] = useState("");
  // Picking an option writes its label into the search box; that is not a search.
  const term = value && search === variantLabel(value) ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({ ...variantSearchQueryOptions(debounced), enabled: debounced !== "" });
  const found = (debounced === "" ? [] : (data ?? [])).filter((variant) => !onlyWithBom || variant.hasBom);
  const options = found.map((variant) => ({ value: String(variant.id), label: variantLabel(variant) }));
  if (value && !options.some((option) => option.value === String(value.id)))
    options.unshift({ value: String(value.id), label: variantLabel(value) });

  return (
    <Select
      label={compact ? undefined : label}
      aria-label={label}
      placeholder={t("typeToSearchVariants")}
      searchable
      disabled={disabled}
      withAsterisk={withAsterisk}
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
