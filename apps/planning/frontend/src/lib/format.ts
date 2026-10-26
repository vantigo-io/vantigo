import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { competenceName } from "../api/competences";
import type { PlanningLoadUnit } from "../api/meta";
import { planningMetaQueryOptions } from "../api/meta";
import type { PlanningPersonRef } from "../api/people";
import type { PlanningProjectRef } from "../api/projects";
import type { PlanningRequestStatus, PlanningRequirement } from "../api/requests";
import "../i18n";
import { formatRange, isoWeek, toUtc } from "./weeks";

/** What an empty cell or an absent value reads. */
export const NONE = "—";

/** A request status's badge colour. */
export const requestStatusColor: Record<PlanningRequestStatus, string> = {
  open: "orange",
  filled: "teal",
  withdrawn: "gray",
};

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
  const { t, formatters, locale } = useI18n("planning");
  const meta = useQuery(planningMetaQueryOptions());
  /** A competence or a requirement by its name, else its key, else its id (rule 4). */
  const nameOfCompetence = (competence: {
    nameEn?: string;
    nameNb?: string;
    key?: string;
    id?: number;
    competenceId?: number;
  }) =>
    competenceName(competence, locale) ??
    competence.key ??
    t("competenceById", { id: competence.id ?? competence.competenceId });
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
    /** The day of an instant (a wish filed or seen), in the reader's time zone. */
    stamp: (instant: string) =>
      formatters.formatDate(new Date(instant), { day: "numeric", month: "short", year: "numeric" }),
    range: (from: string, to: string) => formatRange(from, to, formatters.formatDate),
    week: (monday: string) => t("weekNumber", { week: isoWeek(monday) }),
    personName: (person: Pick<PlanningPersonRef, "displayName">) =>
      person.displayName ?? (meta.data?.workforceVisible === false ? t("aColleague") : t("erasedPerson")),
    projectName: (project: PlanningProjectRef) =>
      project.code && project.name ? `${project.code} ${project.name}` : t("projectById", { id: project.id }),
    competenceName: nameOfCompetence,
    /** A request's requirement: the competence, and the lowest level when it asks for one. */
    requirement: (requirement: PlanningRequirement) =>
      requirement.minLevel === undefined
        ? nameOfCompetence(requirement)
        : t("requirementWithLevel", {
            name: nameOfCompetence(requirement),
            level: t(`level_${requirement.minLevel}`),
          }),
  };
};
