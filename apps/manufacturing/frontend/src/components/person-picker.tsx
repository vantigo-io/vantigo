import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { type ManufacturingPerson, peopleSearchQueryOptions } from "../api/people";
import { useManufacturingFormat } from "../lib/format";

const SEARCH_DEBOUNCE_MS = 250;

export interface PersonPickerProps {
  label: string;
  value: ManufacturingPerson | undefined;
  onChange: (person: ManufacturingPerson | undefined) => void;
  disabled?: boolean;
}

/**
 * A user searched by display name (`GET /manufacturing/people?q`), for an
 * order's responsible. Nothing is listed until something is typed.
 */
export const PersonPicker = ({ label, value, onChange, disabled }: PersonPickerProps) => {
  const { t } = useManufacturingFormat();
  const [search, setSearch] = useState("");
  // Picking an option writes its name into the search box; that is not a search.
  const term = value && search === value.displayName ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({ ...peopleSearchQueryOptions(debounced), enabled: debounced !== "" });
  const found = debounced === "" ? [] : (data ?? []);
  const options = found.map((person) => ({ value: person.id, label: person.displayName }));
  if (value && !options.some((option) => option.value === value.id))
    options.unshift({ value: value.id, label: value.displayName });

  return (
    <Select
      label={label}
      placeholder={t("typeToSearchPeople")}
      searchable
      clearable
      disabled={disabled}
      value={value?.id ?? null}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noPeopleFound")}
      onChange={(next) => {
        if (next === null) return onChange(undefined);
        onChange(found.find((person) => person.id === next) ?? value);
      }}
    />
  );
};
