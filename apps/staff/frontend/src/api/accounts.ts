import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * A person's account (delivery C, design S22 and S27; Entra B, D5, D7, D8):
 * the flag and its dates, which address it would use, the person's account
 * kind, the link the accounts worker drives with identity, the provider link
 * it drives with an external directory, and the directory profile. The
 * person, `staff:manage` and `staff:personal-data` read it; anyone else gets
 * the unknown id's 404.
 */
export type StaffPersonAccount = Schemas["StaffPersonAccount"];
export type StaffPersonAccountInput = Schemas["StaffPersonAccountRequest"];
export type StaffAccountLink = Schemas["StaffAccountLink"];
export type StaffAccountEmailKind = Schemas["StaffAccountEmailKind"];
export type StaffPersonAccountRun = Schemas["StaffPersonAccountRun"];
export type StaffAccountsRun = Schemas["StaffAccountsRun"];
/** `local`, `external` or a provider's key (`entra`): who owns the person's account. */
export type StaffAccountKind = Schemas["StaffAccountKind"];
export type StaffProviderLink = Schemas["StaffProviderLink"];
export type StaffDirectoryField = Schemas["StaffDirectoryField"];
export type StaffAccountPass = Schemas["StaffAccountPass"];

export const personAccountQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "account"],
    queryFn: ({ signal }) => request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account`, { signal }),
  });

/**
 * The flag's fields, a full replace — what is left out is cleared, but for
 * the account kind, which a body that leaves it out keeps — guarded by the
 * flag's own revision, never the person's.
 */
export const updatePersonAccount = (id: number, input: StaffPersonAccountInput, revision: number) =>
  request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account`, json("PUT", input, revision));

/** Run now for one person, with a fresh invitation for a pending user; `invitationSent` says whether one went. */
export const runPersonAccount = (id: number) =>
  request<StaffPersonAccountRun>(`/api/v1/staff/people/${id}/account/run`, { method: "POST" });

/** Disable now: turns the flag off, then disables the user — the flag is off whatever identity answers. */
export const disablePersonAccount = (id: number) =>
  request<StaffPersonAccount>(`/api/v1/staff/people/${id}/account/disable`, { method: "POST" });

/**
 * Shows the pass the person's provider issued for their external account
 * (`staff:manage`), once: a second call, a call after its lifetime and a
 * call when none was issued answer `pass_gone`. Never cached, never fetched
 * again by the page.
 */
export const revealAccountPass = (id: number) =>
  request<StaffAccountPass>(`/api/v1/staff/people/${id}/account/pass`, { method: "POST" });

/** One accounts cycle by hand (`staff:manage`); a run may take minutes when identity or the mail server is slow. */
export const runAccounts = () => request<StaffAccountsRun>("/api/v1/staff/accounts/run", { method: "POST" });
