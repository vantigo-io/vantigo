import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { planningPeopleQueryOptions } from "../api/people";
import "../i18n";
import { personLabel } from "../lib/format";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** Who the field names before anything is searched: a person of the board, or an allocation's person. */
export interface PickedPerson {
  id: number;
  label: string;
}

export interface PersonPickerProps {
  label: string;
  /** The person picked, by id; null for nobody. */
  value: number | null;
  onChange: (personId: number | null) => void;
  /** The person `value` names, so the field can say who it is before anything is searched. */
  selected?: PickedPerson;
  error?: string;
}

/**
 * A person picked from the people of the window as Staff answers them to the
 * caller (`GET /planning/people?q=`, S8) — never Staff's own directory, which
 * this package does not import. Nothing is listed until something is typed.
 */
export const PersonPicker = ({ label, value, onChange, selected, error }: PersonPickerProps) => {
  const { t } = useI18n("planning");
  const [picked, setPicked] = useState<PickedPerson | undefined>(selected);
  const [search, setSearch] = useState("");
  // Picking an option writes its label into the search box; that is not a search.
  const term = picked && search === picked.label ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({
    ...planningPeopleQueryOptions({ q: debounced }),
    enabled: debounced !== "",
  });

  const found = debounced === "" ? [] : (data ?? []);
  const options = found.map((person) => ({ value: String(person.id), label: personLabel(person) }));
  const current = picked && picked.id === value ? picked : undefined;
  if (current && !options.some((option) => option.value === String(current.id)))
    options.unshift({ value: String(current.id), label: current.label });

  return (
    <Select
      label={label}
      placeholder={t("typeToSearchPeople")}
      withAsterisk
      searchable
      value={value === null ? null : String(value)}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      // The server has already matched the words.
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noPeopleFound")}
      error={error}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        const person = found.find((one) => one.id === id);
        setPicked(id === null ? undefined : person ? { id: person.id, label: personLabel(person) } : current);
        onChange(id);
      }}
    />
  );
};
