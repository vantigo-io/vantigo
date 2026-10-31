import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The channel invitations to people outside the company are sent from
 * (integrations design T6): the stored choice, what it is now and the channels
 * a choice may name.
 */
export type InvitationSettings = Schemas["CalendarInvitationSettings"];
export type InvitationChannel = Schemas["CalendarSendChannel"];

const path = "/api/v1/calendar/invitation-settings";

export const invitationSettingsQueryKey = [CALENDAR_QUERY_KEY, "invitation-settings"] as const;

/** Needs `calendar:manage`: the page asks only when the meta says the caller holds it. */
export const invitationSettingsQueryOptions = () =>
  queryOptions({
    queryKey: invitationSettingsQueryKey,
    queryFn: ({ signal }) => request<InvitationSettings>(path, { signal }),
  });

/** A whole replace at the revision it was read at; null is off. */
export const updateInvitationSettings = (
  revision: number,
  invitationChannel: string | null,
): Promise<InvitationSettings> =>
  request<InvitationSettings>(path, json("PUT", { invitationChannel }, { "If-Match": String(revision) }));
