import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic, useLiveUserId } from "@vantigo/frontend-shell";
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

/**
 * Whether the sync waits for the person's own Outlook connection (Outlook
 * connection design M9): the worker's last finding, not_connected or
 * reconnect. The connection's button above the card is what the person
 * does; the sync itself can still be turned on and run.
 */
export const waitsForConnection = (availability: MailboxSyncAvailability | undefined) =>
  availability === "not_connected" || availability === "reconnect";

/** Whether the sync may be turned on or run now: available, or waiting for the person's connection. */
export const mailboxSyncCanRun = (availability: MailboxSyncAvailability | undefined) =>
  availability === "available" || waitsForConnection(availability);

/**
 * How often the card is read again while it waits — for the connection, so a
 * connect shows within the worker's cycle, or for the first cycle after it
 * was turned on (pending), so a parking's Connect hint shows. Only while the
 * live channel is not available: live, the card is refetched on an event.
 */
const WAITING_REFETCH_MS = 15_000;

/** Whether the card is read again: while it waits for the connection, or on and pending its first cycle. */
const waits = (sync: MailboxSync | undefined) =>
  waitsForConnection(sync?.availability) || (sync?.enabled === true && sync.state === "pending");

const path = "/api/v1/calendar/mailbox-sync";

export const mailboxSyncQueryKey = [CALENDAR_QUERY_KEY, "mailbox-sync"] as const;

/**
 * Listens to the caller's own Outlook sync topic (live design K41) and refetches the card on an
 * event or a resync. Returns whether the topic is live: the card polls only while it is not.
 */
export function useMailboxSyncLive(): boolean {
  const client = useQueryClient();
  const me = useLiveUserId();
  return useLiveTopic(me ? `calendar.mailbox-sync:${me}` : null, () => {
    void client.invalidateQueries({ queryKey: mailboxSyncQueryKey });
  });
}

/**
 * The caller's Outlook sync (calendar Outlook sync design O5): the setting, the worker's state and whether it can run.
 * `live` is whether the card's live topic is (useMailboxSyncLive): while it is, there is no poll.
 */
export const mailboxSyncQueryOptions = (live = false) =>
  queryOptions({
    queryKey: mailboxSyncQueryKey,
    queryFn: ({ signal }) => request<MailboxSync>(path, { signal }),
    refetchInterval: (query) => (!live && waits(query.state.data) ? WAITING_REFETCH_MS : false),
  });

/** A whole replace at the revision it was read at — 0 for a caller who never turned it on. */
export const updateMailboxSync = (revision: number, input: MailboxSyncWrite): Promise<MailboxSync> =>
  request<MailboxSync>(path, json("PUT", input, { "If-Match": String(revision) }));

/** Sync now: the sync is made due at once; the setting and its revision stay. */
export const runMailboxSync = (): Promise<MailboxSync> => request<MailboxSync>(`${path}/run`, { method: "POST" });
