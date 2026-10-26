import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type PlanningProject, type PlanningProjectStatus, planningProjectsQueryOptions } from "../api/projects";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** Who the field names before anything is searched: the board's project, or an allocation's. */
export interface PickedProject {
  id: number;
  label: string;
  status?: PlanningProjectStatus;
}

export interface ProjectPickerProps {
  label: string;
  value: number | null;
  /** The project picked, with its status when the list answered it — the status defaults by it (S3). */
  onChange: (projectId: number | null, project?: PlanningProject) => void;
  selected?: PickedProject;
  error?: string;
  /** Optional instead of required: the board's filter. */
  clearable?: boolean;
  placeholder?: string;
}

/**
 * A plannable project (`GET /planning/projects?q=`, S9: planned, active and
 * on hold, by code). The list is short enough to show before anything is
 * typed; a typed word narrows it on the server.
 */
export const ProjectPicker = ({
  label,
  value,
  onChange,
  selected,
  error,
  clearable,
  placeholder,
}: ProjectPickerProps) => {
  const { t } = useI18n("planning");
  const [picked, setPicked] = useState<PickedProject | undefined>(selected);
  const [search, setSearch] = useState("");
  const term = picked && search === picked.label ? "" : search.trim();
  const [debounced] = useDebouncedValue(term, SEARCH_DEBOUNCE_MS);
  const { data, isFetching } = useQuery(planningProjectsQueryOptions({ q: debounced || undefined }));

  const found = data ?? [];
  const labelOf = (project: PlanningProject) => `${project.code} ${project.name}`;
  const options = found.map((project) => ({ value: String(project.id), label: labelOf(project) }));
  const current = picked && picked.id === value ? picked : undefined;
  if (current && !options.some((option) => option.value === String(current.id)))
    options.unshift({ value: String(current.id), label: current.label });

  return (
    <Select
      label={label}
      placeholder={placeholder ?? t("typeToSearchProjects")}
      withAsterisk={!clearable}
      clearable={clearable}
      searchable
      value={value === null ? null : String(value)}
      searchValue={search}
      onSearchChange={setSearch}
      data={options}
      filter={({ options: all }) => all}
      nothingFoundMessage={isFetching ? undefined : t("noProjectsFound")}
      error={error}
      onChange={(next) => {
        const id = next === null ? null : Number(next);
        const project = found.find((one) => one.id === id);
        setPicked(
          id === null ? undefined : project ? { id, label: labelOf(project), status: project.status } : current,
        );
        onChange(id, project);
      }}
    />
  );
};
