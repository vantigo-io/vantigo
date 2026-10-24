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

/** The evidence's bytes, through the documents' own download. */
export const downloadEvidence = (evidence: StaffCompetenceEvidence): Promise<FileDownload> =>
  downloadFile(`/api/v1/staff/documents/${evidence.documentId}`, evidence.fileName);
