import type { components } from "../api-schema";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** What the RSVP page shows an invitee (design D5): nothing more than they need to decide. */
export type RsvpPageView = Schemas["RsvpPage"];
export type RsvpAnswer = Schemas["RsvpWrite"]["status"];

/*
 * The RSVP page's two anonymous operations — the only requests the page
 * makes, both on the public host's allowlist. Neither needs a session, so a
 * 401 never signs anyone out. The token travels only in a request's body,
 * never in an API URL: the access logs of every proxy in front of the API see
 * the paths, not the bodies.
 */

/** Reads the appointment an RSVP link names: `POST /rsvp/read`, the token in the body. */
export const readRsvpPage = (token: string): Promise<RsvpPageView> =>
  request<RsvpPageView>("/api/v1/calendar/rsvp/read", { ...json("POST", { token }), handleUnauthorized: false });

/** The invitee's answer: `POST /rsvp`, the token and the answer in the body. */
export const answerRsvp = (token: string, status: RsvpAnswer): Promise<RsvpPageView> =>
  request<RsvpPageView>("/api/v1/calendar/rsvp", { ...json("POST", { token, status }), handleUnauthorized: false });
