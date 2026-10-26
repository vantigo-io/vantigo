import { Select } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { competenceKinds, competencesQueryOptions, type PlanningCompetence } from "../api/competences";
import { usePlanningFormat } from "../lib/format";

export interface CompetencePickerProps {
  label: string;
  value: number | null;
  /** The competence picked, with its catalogue entry — `hasLevel` decides whether a level is asked for. */
  onChange: (competenceId: number | null, competence?: PlanningCompetence) => void;
  error?: string;
  /** Optional instead of required: the board's filter. */
  clearable?: boolean;
  placeholder?: string;
  /** Competences not to offer: the request's other requirements, each competence once (S16). */
  exclude?: readonly number[];
}

/**
 * A competence of Staff's catalogue (`GET /planning/competences`, amendment
 * 38) — this package's own, since a module frontend imports no other (rule
 * 7). The catalogue is short, so it is listed whole, grouped by kind and
 * searched in the browser. A hidden competence is offered for nothing new,
 * but the value already chosen keeps its name.
 */
export const CompetencePicker = ({
  label,
  value,
  onChange,
  error,
  clearable,
  placeholder,
  exclude = [],
}: CompetencePickerProps) => {
  const format = usePlanningFormat();
  const { t } = format;
  const { data } = useQuery(competencesQueryOptions());
  const catalogue = data ?? [];
  const offered = catalogue.filter(
    (competence) => competence.id === value || (!competence.hidden && !exclude.includes(competence.id)),
  );
  const groups = competenceKinds
    .map((kind) => ({
      group: t(`competenceKind_${kind}`),
      items: offered
        .filter((competence) => competence.kind === kind)
        .map((competence) => ({ value: String(competence.id), label: format.competenceName(competence) }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    }))
    .filter((group) => group.items.length > 0);
  // A value the catalogue does not answer (a link, or an archived competence) still shows as something.
  if (value !== null && !catalogue.some((competence) => competence.id === value))
    groups.push({
      group: t("competence"),
      items: [{ value: String(value), label: t("competenceById", { id: value }) }],
    });

  return (
    <Select
      label={label}
      placeholder={placeholder ?? t("typeToSearchCompetences")}
      withAsterisk={!clearable}
      clearable={clearable}
      allowDeselect={clearable === true}
      searchable
      value={value === null ? null : String(value)}
      data={groups}
      nothingFoundMessage={t("noCompetencesFound")}
      error={error}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        onChange(
          id,
          catalogue.find((competence) => competence.id === id),
        );
      }}
    />
  );
};
