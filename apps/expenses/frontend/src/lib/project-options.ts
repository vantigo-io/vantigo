import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ExpenseProjectOption, expenseProjectsQueryOptions, type ProjectPicker } from "../api/projects";
import "../i18n";

/** The project an expense or a travel claim is already booked on, as the server renders it. */
export interface StoredProject {
  id: number;
  code: string;
  name: string;
}

export interface ProjectOptions {
  /** What `GET /projects` answered, or undefined while it is still in flight. */
  projects: ExpenseProjectOption[] | undefined;
  /** What the picker offers, the stored project included when it is no longer bookable. */
  options: { value: string; label: string }[];
}

/**
 * The options a project picker offers.
 *
 * `GET /projects` lists only what the owner may book on **now**, while a save
 * grandfathers a link the record already carries — a completed project, or
 * one the owner has been taken off. Without the stored project in the list
 * the picker would render blank over "No project" and the form would tell
 * somebody their booked cost is unbooked.
 *
 * `projects === undefined` is "still loading", not "does not offer it": a
 * perfectly bookable project must not flash "(no longer bookable)" between
 * the mount and the answer.
 *
 * One function rather than one per form: the expense modal, the travel
 * claim's header and anything later that books something all need exactly
 * this, and twenty lines with a loading-state invariant in them are the sort
 * of thing that drifts when it is copied.
 */
export const useProjectOptions = (
  stored: StoredProject | undefined,
  enabled: boolean,
  kind?: ProjectPicker,
): ProjectOptions => {
  const { t } = useI18n("expenses");
  const { data: projects } = useQuery({ ...expenseProjectsQueryOptions(kind), enabled });

  const kept =
    stored && projects !== undefined && !projects.some((project) => project.id === stored.id) ? stored : undefined;

  return {
    projects,
    options: [
      ...(projects ?? []).map((project) => ({ value: String(project.id), label: `${project.code} · ${project.name}` })),
      ...(kept
        ? [
            {
              value: String(kept.id),
              label: t("projectNoLongerBookable", { project: `${kept.code} · ${kept.name}` }),
            },
          ]
        : []),
    ],
  };
};
