import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { sendAbsenceWrite } from "./absence";
import { type Paginated, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/** A sick-leave case as the caller may read it: no diagnosis anywhere, a milestone's note only to its readers. */
export type StaffCase = Schemas["StaffCase"];
export type StaffCaseMilestone = Schemas["StaffCaseMilestone"];
export type StaffMilestoneKind = Schemas["StaffMilestoneKind"];

/** The open cases the caller may read, newest first. */
export const openCasesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "cases", "open"],
    queryFn: ({ signal }) => request<Paginated<StaffCase>>("/api/v1/staff/cases?status=open&pageSize=100", { signal }),
  });

/** Marks a milestone done today, clearing a waiver. */
export const markMilestoneDone = (caseId: number, kind: StaffMilestoneKind): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(`/api/v1/staff/cases/${caseId}/milestones/${kind}/done`, "POST", {});

/** Waives a milestone as plainly unnecessary, with the reason. */
export const waiveMilestone = (caseId: number, kind: StaffMilestoneKind, reason: string): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(`/api/v1/staff/cases/${caseId}/milestones/${kind}/waive`, "POST", { reason });

/** The accommodation note — the manager's and HR's — guarded by the case's revision; empty clears it. */
export const updateAccommodationNote = (sickCase: StaffCase, note: string): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(
    `/api/v1/staff/cases/${sickCase.id}`,
    "PUT",
    { accommodationNote: note.trim() === "" ? null : note },
    String(sickCase.revision),
  );

/** A person's sick-leave cases the caller may read, open or all. */
export const personCasesQueryOptions = (personId: number, status: "open" | "all" = "open") =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "cases", "person", personId, status],
    queryFn: ({ signal }) =>
      request<Paginated<StaffCase>>(`/api/v1/staff/cases?personId=${personId}&status=${status}&pageSize=100`, {
        signal,
      }),
  });

/** HR's estimate of the 52-week end; null returns it to the derived first day + 364. */
export const updateMaxBenefitEstimate = (sickCase: StaffCase, estimate: string | null): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(
    `/api/v1/staff/cases/${sickCase.id}`,
    "PUT",
    { maxBenefitEstimate: estimate },
    String(sickCase.revision),
  );

/** HR closes a case before sixteen days have passed since its last day. */
export const closeCase = (caseId: number): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(`/api/v1/staff/cases/${caseId}/close`, "POST");

/** A milestone's note — staff:personal-data's — guarded by the milestone's revision; empty clears it. */
export const updateMilestoneNote = (caseId: number, milestone: StaffCaseMilestone, note: string): Promise<StaffCase> =>
  sendAbsenceWrite<StaffCase>(
    `/api/v1/staff/cases/${caseId}/milestones/${milestone.kind}/note`,
    "PUT",
    note.trim() ? { note } : {},
    String(milestone.revision),
  );
