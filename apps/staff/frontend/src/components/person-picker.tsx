import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { peopleQueryOptions, type StaffPersonSummary } from "../api/people";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

export interface PersonPickerProps {
  label: string;
  description?: string;
  /** The person picked, by id; null for nobody. */
  value: number | null;
  onChange: (personId: number | null) => void;
  /** The person `value` names, so the field can say who it is before anything is searched. */
  selected?: StaffPersonSummary;
  error?: string;
}

const labelOf = (person: StaffPersonSummary) => `${person.displayName} (${person.employeeNumber})`;

/**
 * A person picked from the directory — a department's or a terms row's
 * manager is a *person* in the register, not a user, so this searches
 * `GET /people?q=` (the current people: active, upcoming and not yet employed,
 * as `staff:manage` lists them) rather than the user directory.
 *
 * Nothing is listed until something is typed: a company of three hundred is
 * not a dropdown, and the server already matches each word against the
 * names, the employee number and the work email.
 */
export const PersonPicker = ({ label, description, value, onChange, selected, error }: PersonPickerProps) => {
  const { t } = useI18n("staff");
  const [picked, setPicked] = useState<StaffPersonSummary | undefined>(selected);
  // `selected` arrives after the form has mounted when the name is read from
  // the server; a person picked here meanwhile wins. State adjusted during
  // render from the previous render's value, the way React documents.
  const [seen, setSeen] = useState(selected);
  if (seen !== selected) {
    setSeen(selected);
    if (selected && (picked === undefined || picked.id === selected.id)) setPicked(selected);
  }
  const [search, setSearch] = useState("");
  // Picking an option writes its label into the search box; that is not a
  // search for somebody called "Ada Lovelace (E-1)".
  const term = picked && search === labelOf(picked) ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery({
    ...peopleQueryOptions({ q: debounced, pageSize: 20 }),
    enabled: debounced !== "",
  });

  const found: StaffPersonSummary[] = debounced === "" ? [] : (data?.data ?? []);
  const options = found.map((person) => ({ value: String(person.id), label: labelOf(person) }));
  const current = picked && picked.id === value ? picked : undefined;
  if (current && !options.some((option) => option.value === String(current.id))) {
    options.unshift({ value: String(current.id), label: labelOf(current) });
  }

  return (
    <Select
      label={label}
      description={description}
      placeholder={t("typeToSearchPeople")}
      searchable
      clearable
      value={value === null ? null : String(value)}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      // The server has already matched the words; filtering its answer again
      // by the label would drop a match on the work email.
      filter={({ options: all }) => all}
      nothingFoundMessage={debounced === "" || isFetching ? undefined : t("noPeopleFound")}
      error={error}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        setPicked(id === null ? undefined : (found.find((person) => person.id === id) ?? current));
        onChange(id);
      }}
    />
  );
};
