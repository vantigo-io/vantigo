import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * A person's account (delivery C, design S22 and S27): the flag and its
 * dates, which address it would use, the authority mode and the link the
 * accounts worker drives. The person, `staff:manage` and
 * `staff:personal-data` read it; anyone else gets the unknown id's 404.
 */
export type StaffPersonAccount = Schemas["StaffPersonAccount"];
export type StaffPersonAccountInput = Schemas["StaffPersonAccountRequest"];
export type StaffAccountLink = Schemas["StaffAccountLink"];
export type StaffAccountEmailKind = Schemas["StaffAccountEmailKind"];
export type StaffPersonAccountRun = Schemas["StaffPersonAccountRun"];
export type StaffAccountsRun = Schemas["StaffAccountsRun"];

export const personAccountQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "account"],
    queryFn: ({ signal }) => request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account`, { signal }),
  });

/**
 * The flag's three fields, a full replace — what is left out is cleared —
 * guarded by the flag's own revision, never the person's.
 */
export const updatePersonAccount = (id: number, input: StaffPersonAccountInput, revision: number) =>
  request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account`, json("PUT", input, revision));

/** Run now for one person, with a fresh invitation for a pending user; `invitationSent` says whether one went. */
export const runPersonAccount = (id: number) =>
  request<StaffPersonAccountRun>(`/api/v1/staff/people/${id}/account/run`, { method: "POST" });

/** Disable now: turns the flag off, then disables the user — the flag is off whatever identity answers. */
export const disablePersonAccount = (id: number) =>
  request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account/disable`, { method: "POST" });

/** One accounts cycle by hand (`staff:manage`); a run may take minutes when identity or the mail server is slow. */
export const runAccounts = () => request<StaffAccountsRun>("/api/v1/staff/accounts/run", { method: "POST" });
