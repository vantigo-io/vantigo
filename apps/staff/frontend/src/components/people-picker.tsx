import { MultiSelect } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { peopleQueryOptions } from "../api/people";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** A person as the picker names them: what a group's member and owner rows carry. */
export interface PickedPerson {
  id: number;
  displayName: string;
  employeeNumber: string;
}

export interface PeoplePickerProps {
  label: string;
  description?: string;
  /** The people picked, in the order they were picked. */
  value: PickedPerson[];
  onChange: (people: PickedPerson[]) => void;
  error?: string;
  disabled?: boolean;
}

const labelOf = (person: PickedPerson) => `${person.displayName} (${person.employeeNumber})`;

/**
 * Several people picked from the directory — a group's members or owners —
 * the multi-select sibling of `PersonPicker`: it searches `GET /people?q=`
 * as that one does, nothing listed until something is typed, and keeps the
 * people picked by name, so the pills say who they are without a read each.
 */
export const PeoplePicker = ({ label, description, value, onChange, error, disabled }: PeoplePickerProps) => {
  const { t } = useI18n("staff");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search.trim(), SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({
    ...peopleQueryOptions({ q: debounced, pageSize: 20 }),
    enabled: debounced !== "",
  });
  const found: PickedPerson[] = debounced === "" ? [] : (data?.data ?? []);
  const known = new Map<number, PickedPerson>(value.map((person) => [person.id, person]));
  for (const person of found) if (!known.has(person.id)) known.set(person.id, person);
  // The people picked stay options, so their pills keep their names; the
  // found ones follow.
  const options = [...known.values()].map((person) => ({ value: String(person.id), label: labelOf(person) }));

  return (
    <MultiSelect
      label={label}
      description={description}
      placeholder={value.length === 0 ? t("typeToSearchPeople") : undefined}
      searchable
      clearable
      disabled={disabled}
      value={value.map((person) => String(person.id))}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      // The server has already matched the words, the work email too.
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noPeopleFound")}
      error={error}
      onChange={(ids) => {
        onChange(ids.flatMap((id) => known.get(Number(id)) ?? []));
        setSearch("");
      }}
    />
  );
};
