import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

export type MailboxSync = Schemas["MailboxSync"];
export type MailboxSyncWrite = Schemas["MailboxSyncWrite"];
export type MailboxSyncTarget = Schemas["MailboxSyncTarget"];
export type MailboxSyncDetail = Schemas["MailboxSyncDetail"];
export type MailboxSyncAvailability = MailboxSync["availability"];

/** The refusal code of turning the sync on, or running it, while it cannot run; its detail is the availability. */
export const MAILBOX_SYNC_UNAVAILABLE = "mailbox_sync_unavailable";

const path = "/api/v1/calendar/mailbox-sync";

export const mailboxSyncQueryKey = [CALENDAR_QUERY_KEY, "mailbox-sync"] as const;

/** The caller's Outlook sync (calendar Outlook sync design O5): the setting, the worker's state and whether it can run. */
export const mailboxSyncQueryOptions = () =>
  queryOptions({
    queryKey: mailboxSyncQueryKey,
    queryFn: ({ signal }) => request<MailboxSync>(path, { signal }),
  });

/** A whole replace at the revision it was read at — 0 for a caller who never turned it on. */
export const updateMailboxSync = (revision: number, input: MailboxSyncWrite): Promise<MailboxSync> =>
  request<MailboxSync>(path, json("PUT", input, { "If-Match": String(revision) }));

/** Sync now: the sync is made due at once; the setting and its revision stay. */
export const runMailboxSync = (): Promise<MailboxSync> => request<MailboxSync>(`${path}/run`, { method: "POST" });
