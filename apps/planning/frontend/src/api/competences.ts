import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningCompetence = Schemas["PlanningCompetence"];
export type PlanningCompetenceKind = PlanningCompetence["kind"];

export const competenceKinds: readonly PlanningCompetenceKind[] = ["skill", "certificate"];

/** The levels a requirement may ask for (S16), as Staff grades a skill. */
export const competenceLevels = [0, 1, 2, 3, 4] as const;

/**
 * Staff's competence catalogue as the caller may see it (amendment 38): for a
 * request's requirements and the board's filter. Empty when Staff answers the
 * caller nothing; a hidden competence is listed so an old requirement keeps
 * its name, but offered for no new one.
 */
export const competencesQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "competences"],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningCompetenceList"]>("/api/v1/planning/competences", { signal }).then(
        (answer) => answer.competences,
      ),
  });

/** A competence's name in the reader's language; undefined when the catalogue no longer names it (rule 4). */
export const competenceName = (competence: { nameEn?: string; nameNb?: string }, locale: string): string | undefined =>
  (locale === "nb" ? competence.nameNb : competence.nameEn) || competence.nameEn || undefined;
