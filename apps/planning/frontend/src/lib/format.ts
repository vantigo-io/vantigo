import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import type { PlanningLoadUnit } from "../api/meta";
import { planningMetaQueryOptions } from "../api/meta";
import type { PlanningPersonRef } from "../api/people";
import type { PlanningProjectRef } from "../api/projects";
import "../i18n";
import { formatRange, isoWeek, toUtc } from "./weeks";

/** A person as a picker lists them: the name and the employee number. */
export const personLabel = (person: { displayName: string; employeeNumber: string }) =>
  `${person.displayName} (${person.employeeNumber})`;

/**
 * The package's words for its figures, in the reader's locale: hours and
 * percentages, a load in its unit, a period, a week — and a person or a
 * project the server could not name (rule 4). A person Staff does not answer
 * reads as *Erased person*; when Staff answers nobody to this caller at all
 * (`workforceVisible: false`) the same `null` is *A colleague*, since the
 * caller simply may not see them.
 */
export const usePlanningFormat = () => {
  const { t, formatters } = useI18n("planning");
  const meta = useQuery(planningMetaQueryOptions());
  const number = (value: number, digits = 1) => formatters.formatNumber(value, { maximumFractionDigits: digits });
  return {
    t,
    formatters,
    number,
    hours: (value: number) => t("hoursValue", { value: number(value) }),
    percent: (value: number) => t("percentValue", { value: number(value, 0) }),
    load: (value: number, unit: PlanningLoadUnit) =>
      unit === "percent"
        ? t("loadPercentValue", { value: number(value, 2) })
        : t("loadHoursValue", { value: number(value, 2) }),
    date: (value: string) =>
      formatters.formatDate(toUtc(value), { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" }),
    range: (from: string, to: string) => formatRange(from, to, formatters.formatDate),
    week: (monday: string) => t("weekNumber", { week: isoWeek(monday) }),
    personName: (person: Pick<PlanningPersonRef, "displayName">) =>
      person.displayName ?? (meta.data?.workforceVisible === false ? t("aColleague") : t("erasedPerson")),
    projectName: (project: PlanningProjectRef) =>
      project.code && project.name ? `${project.code} ${project.name}` : t("projectById", { id: project.id }),
  };
};
