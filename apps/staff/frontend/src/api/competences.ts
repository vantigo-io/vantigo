import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { sendAbsenceWrite } from "./absence";
import { downloadFile, type FileDownload } from "./documents";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffCompetenceCategory = Schemas["StaffCompetenceCategory"];
export type StaffCompetenceCategoryInput = Schemas["StaffCompetenceCategoryRequest"];
export type StaffCompetenceCategoryChange = Schemas["StaffCompetenceCategoryChangeRequest"];
export type StaffCompetence = Schemas["StaffCompetence"];
export type StaffCompetenceKind = Schemas["StaffCompetenceKind"];
export type StaffCompetenceInput = Schemas["StaffCompetenceRequest"];
export type StaffCompetenceChange = Schemas["StaffCompetenceChangeRequest"];
export type StaffCompetenceSummary = Schemas["StaffCompetenceSummary"];
export type StaffPersonCompetence = Schemas["StaffPersonCompetence"];
export type StaffCompetenceEvidence = Schemas["StaffCompetenceEvidence"];
export type StaffPersonCompetenceInput = Schemas["StaffPersonCompetenceRequest"];
export type StaffPersonCompetenceChange = Schemas["StaffPersonCompetenceChangeRequest"];
export type StaffCompetenceVerify = Schemas["StaffCompetenceVerifyRequest"];
export type StaffCompetenceRenew = Schemas["StaffCompetenceRenewRequest"];
export type StaffPersonInterest = Schemas["StaffPersonInterest"];
export type StaffPersonInterestsInput = Schemas["StaffPersonInterestsRequest"];
export type StaffPreferenceKind = Schemas["StaffPreferenceKind"];
export type StaffPersonPreference = Schemas["StaffPersonPreference"];
export type StaffPersonPreferencesInput = Schemas["StaffPersonPreferencesRequest"];
export type StaffAvailabilityKind = Schemas["StaffAvailabilityKind"];
export type StaffAvailabilityDeclaration = Schemas["StaffAvailabilityDeclaration"];
export type StaffAvailabilityInput = Schemas["StaffAvailabilityRequest"];
export type StaffWishesSeen = Schemas["StaffWishesSeen"];
export type StaffCompetenceReview = Schemas["StaffCompetenceReview"];
export type StaffCompetenceReviewInput = Schemas["StaffCompetenceReviewRequest"];
export type StaffCompetenceProfile = Schemas["StaffCompetenceProfile"];
export type StaffCompetenceProfileSummary = Schemas["StaffCompetenceProfileSummary"];
export type StaffCompetenceProfileItem = Schemas["StaffCompetenceProfileItem"];
export type StaffCompetenceProfileItemInput = Schemas["StaffCompetenceProfileItemRequest"];
export type StaffCompetenceProfileInput = Schemas["StaffCompetenceProfileRequest"];
export type StaffCompetenceProfileChange = Schemas["StaffCompetenceProfileChangeRequest"];
export type StaffPersonProfile = Schemas["StaffPersonProfile"];
export type StaffPersonProfileList = Schemas["StaffPersonProfileList"];
export type StaffCompetenceGaps = Schemas["StaffCompetenceGaps"];
export type StaffCompetenceGapProfile = Schemas["StaffCompetenceGapProfile"];
export type StaffCompetenceGapItem = Schemas["StaffCompetenceGapItem"];
export type StaffCompetenceGapStatus = Schemas["StaffCompetenceGapStatus"];
export type StaffCompetenceMatrix = Schemas["StaffCompetenceMatrix"];
export type StaffCompetenceMatrixCell = Schemas["StaffCompetenceMatrixCell"];
export type StaffCompetenceInterestsEntry = Schemas["StaffCompetenceInterestsEntry"];

/** The matrix's five filters (design S14), as `GET /competence-matrix` takes them; each left out is no filter. */
export interface StaffCompetenceMatrixFilters {
  categoryId?: number;
  competenceId?: number[];
  minLevel?: number;
  validOn?: string;
  verifiedOnly?: boolean;
}

/** Every competence category in order, archived ones included, so a competence still names its own. */
export const competenceCategoriesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "competence-categories"],
    queryFn: ({ signal }) => request<StaffCompetenceCategory[]>("/api/v1/staff/competence-categories", { signal }),
  });

/**
 * The competence catalogue. The hidden and archived rows are listed to
 * `staff:manage` alone, and only when asked for; to anyone else the server
 * ignores the two and answers what may be picked for a new row.
 */
export const competencesQueryOptions = ({
  includeHidden = false,
  includeArchived = false,
}: {
  includeHidden?: boolean;
  includeArchived?: boolean;
} = {}) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "competences", { includeHidden, includeArchived }],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams();
      if (includeHidden) params.set("includeHidden", "true");
      if (includeArchived) params.set("includeArchived", "true");
      const query = params.toString();
      return request<StaffCompetence[]>(`/api/v1/staff/competences${query ? `?${query}` : ""}`, { signal });
    },
  });

/**
 * A person's competences — the current rows, or with `history` every row,
 * newest first. My page calls it with the linked person's id, as it calls
 * the rest; who reads what is the server's (a colleague without the detail).
 */
export const personCompetencesQueryOptions = (personId: number, history = false) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "competences", { history }],
    queryFn: ({ signal }) =>
      request<StaffPersonCompetence[]>(
        `/api/v1/staff/people/${personId}/competences${history ? "?history=true" : ""}`,
        { signal },
      ),
  });

/*
 * What the person wants (delivery B, design S8–S10): three lists the person
 * alone writes, each whole; read by them, their manager, HR and
 * `staff:personal-data` — anyone else gets the unknown person's 404.
 */

export const interestsQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "interests"],
    queryFn: ({ signal }) =>
      request<Schemas["StaffPersonInterestList"]>(`/api/v1/staff/people/${personId}/interests`, { signal }),
  });

export const preferencesQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "preferences"],
    queryFn: ({ signal }) =>
      request<Schemas["StaffPersonPreferenceList"]>(`/api/v1/staff/people/${personId}/preferences`, { signal }),
  });

export const availabilityQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "availability"],
    queryFn: ({ signal }) =>
      request<Schemas["StaffAvailabilityList"]>(`/api/v1/staff/people/${personId}/availability`, { signal }),
  });

/** The reviews recorded with the person (design S11), newest first; a colleague gets the 404. */
export const competenceReviewsQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "competence-reviews"],
    queryFn: ({ signal }) =>
      request<StaffCompetenceReview[]>(`/api/v1/staff/people/${personId}/competence-reviews`, { signal }),
  });

/*
 * Profiles and gaps (delivery C, design S12–S14). The catalogue of profiles
 * is read by anyone in the app, archived ones included; a person's profiles
 * and gaps by the Profiles group's readers — the person, their manager, an
 * Approver, HR and staff:personal-data — anyone else gets the unknown
 * person's 404, which the tab reads as the group's absence.
 */

/** Every competence profile with its items, archived ones included (`archivedAt`), ordered by key. */
export const competenceProfilesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "competence-profiles"],
    queryFn: ({ signal }) => request<StaffCompetenceProfile[]>("/api/v1/staff/competence-profiles", { signal }),
  });

/** The profiles that reach a person: assigned, their department's default, or both (plan reading 25). */
export const personProfilesQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "competence-profiles"],
    queryFn: ({ signal }) =>
      request<StaffPersonProfileList>(`/api/v1/staff/people/${personId}/competence-profiles`, { signal }),
  });

/** Per profile and item, what the person's current rows meet (design S13), computed by the server on read. */
export const competenceGapsQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "competence-gaps"],
    queryFn: ({ signal }) =>
      request<StaffCompetenceGaps>(`/api/v1/staff/people/${personId}/competence-gaps`, { signal }),
  });

/** The matrix (design S14): the people active today and their rows valid on the day, filtered as asked. */
export const competenceMatrixQueryOptions = (filters: StaffCompetenceMatrixFilters) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "competence-matrix", filters],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams();
      if (filters.categoryId !== undefined) params.set("categoryId", String(filters.categoryId));
      for (const id of filters.competenceId ?? []) params.append("competenceId", String(id));
      if (filters.minLevel !== undefined) params.set("minLevel", String(filters.minLevel));
      if (filters.validOn) params.set("validOn", filters.validOn);
      if (filters.verifiedOnly) params.set("verifiedOnly", "true");
      const query = params.toString();
      return request<StaffCompetenceMatrix>(`/api/v1/staff/competence-matrix${query ? `?${query}` : ""}`, {
        signal,
      });
    },
  });

/** Who wants to learn what, and who wants to use more of what they hold — staff:manage's alone. */
export const competenceInterestsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "competence-interests"],
    queryFn: ({ signal }) => request<StaffCompetenceInterestsEntry[]>("/api/v1/staff/competence-interests", { signal }),
  });

export const createCompetenceCategory = (input: StaffCompetenceCategoryInput): Promise<StaffCompetenceCategory> =>
  request<StaffCompetenceCategory>("/api/v1/staff/competence-categories", json("POST", input));

/** A company category's names and whether it is archived; refused `seed_immutable` and `category_in_use` (409). */
export const updateCompetenceCategory = (
  id: number,
  input: StaffCompetenceCategoryChange,
  revision: number,
): Promise<StaffCompetenceCategory> =>
  request<StaffCompetenceCategory>(`/api/v1/staff/competence-categories/${id}`, json("PUT", input, revision));

export const createCompetence = (input: StaffCompetenceInput): Promise<StaffCompetence> =>
  request<StaffCompetence>("/api/v1/staff/competences", json("POST", input));

/** A competence replaced but for its key and kind; a seed's fixed fields are refused `seed_immutable`. */
export const updateCompetence = (
  id: number,
  input: StaffCompetenceChange,
  revision: number,
): Promise<StaffCompetence> =>
  request<StaffCompetence>(`/api/v1/staff/competences/${id}`, json("PUT", input, revision));

/*
 * A person's row's writes go through the absence module's sender, which
 * keeps a validation refusal's code (`level_required`, `competence_unavailable`)
 * that the shared client drops: the form words those by their code.
 */

/** Declares (the person) or adds (a verifier, HR) a competence; `competence_held` (409) when a current row has it. */
export const declareCompetence = (
  personId: number,
  input: StaffPersonCompetenceInput,
): Promise<StaffPersonCompetence> =>
  sendAbsenceWrite<StaffPersonCompetence>(`/api/v1/staff/people/${personId}/competences`, "POST", input);

/** A row's level, dates, issuer and reference, replaced as a whole under `If-Match`. */
export const changeCompetence = (
  id: number,
  input: StaffPersonCompetenceChange,
  revision: number,
): Promise<StaffPersonCompetence> =>
  sendAbsenceWrite<StaffPersonCompetence>(`/api/v1/staff/person-competences/${id}`, "PUT", input, String(revision));

/** Removes a current row; `chain` (HR's) the superseded rows of the competence with it. */
export const removeCompetence = (id: number, chain = false): Promise<void> =>
  sendAbsenceWrite<void>(`/api/v1/staff/person-competences/${id}${chain ? "?chain=true" : ""}`, "DELETE");

/** The verifier's stamp: the level agreed, the basis and an optional note; `own_record` (403) for the person. */
export const verifyCompetence = (id: number, input: StaffCompetenceVerify): Promise<StaffPersonCompetence> =>
  sendAbsenceWrite<StaffPersonCompetence>(`/api/v1/staff/person-competences/${id}/verify`, "POST", input);

/** A new row superseding the current one, which stays as history. */
export const renewCompetence = (id: number, input: StaffCompetenceRenew): Promise<StaffPersonCompetence> =>
  sendAbsenceWrite<StaffPersonCompetence>(`/api/v1/staff/person-competences/${id}/renew`, "POST", input);

/**
 * Files a row's evidence in the person's archive (the `certificate`
 * category) and names it on the row: one file in the part `file`, the
 * `Content-Type` left to the browser. Refused as a document upload is.
 */
export const uploadEvidence = (id: number, file: File): Promise<StaffPersonCompetence> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<StaffPersonCompetence>(`/api/v1/staff/person-competences/${id}/evidence`, {
    method: "POST",
    body: form,
  });
};

/**
 * A row's evidence file, read through the row rather than the archive, so
 * everyone who reads the row's detail — the person, their manager, an
 * Approver, HR, `staff:personal-data` — may download it; anyone else gets
 * the unknown row's 404. Named as the server names it, `fileName` failing that.
 */
export const downloadEvidence = (rowId: number, fileName: string): Promise<FileDownload> =>
  downloadFile(`/api/v1/staff/person-competences/${rowId}/evidence`, fileName);

/*
 * The wishes' writes (delivery B) go through the same sender, so a refusal
 * keyed `interests[2].competenceId` keeps its code (`competence_unavailable`).
 * No If-Match: the person is the lists' only writer (plan reading 20).
 */

/** The person's interests, replaced whole (at most 20). */
export const replaceInterests = (
  personId: number,
  input: StaffPersonInterestsInput,
): Promise<Schemas["StaffPersonInterestList"]> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/interests`, "PUT", input);

/** The person's preferences, one per kind, replaced whole. */
export const replacePreferences = (
  personId: number,
  input: StaffPersonPreferencesInput,
): Promise<Schemas["StaffPersonPreferenceList"]> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/preferences`, "PUT", input);

/** The person's availability declaration, replaced whole (at most 50 rows). */
export const replaceAvailability = (
  personId: number,
  input: StaffAvailabilityInput,
): Promise<Schemas["StaffAvailabilityList"]> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/availability`, "PUT", input);

/** The manager's or HR's Seen: every unstamped wish and current competence row stamped, the counts answered. */
export const markWishesSeen = (personId: number): Promise<StaffWishesSeen> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/wishes/seen`, "POST");

/** Records that the two went through the Competence tab together; today when no day is given. */
export const recordCompetenceReview = (
  personId: number,
  input: StaffCompetenceReviewInput = {},
): Promise<StaffCompetenceReview> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/competence-reviews`, "POST", input);

/*
 * The profiles' writes (delivery C) go through the same sender, so a refusal
 * keyed `items[1].minLevel` keeps its code (`level_not_allowed`,
 * `competence_unavailable`) for the form to word.
 */

/** A new profile: its key (fixed for good), names and items. */
export const createCompetenceProfile = (input: StaffCompetenceProfileInput): Promise<StaffCompetenceProfile> =>
  sendAbsenceWrite("/api/v1/staff/competence-profiles", "POST", input);

/** A profile's names, whether it is archived and its items, replaced whole under `If-Match`. */
export const updateCompetenceProfile = (
  id: number,
  input: StaffCompetenceProfileChange,
  revision: number,
): Promise<StaffCompetenceProfile> =>
  sendAbsenceWrite(`/api/v1/staff/competence-profiles/${id}`, "PUT", input, String(revision));

/** The profiles HR assigns the person, replaced whole (at most 20); no If-Match — HR is the only writer. */
export const replacePersonProfiles = (personId: number, profileIds: number[]): Promise<StaffPersonProfileList> =>
  sendAbsenceWrite(`/api/v1/staff/people/${personId}/competence-profiles`, "PUT", { profileIds });
