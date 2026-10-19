import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

export type AppointmentOccurrence = Schemas["AppointmentOccurrence"];
export type Appointment = Schemas["Appointment"];
export type AppointmentWrite = Schemas["AppointmentWrite"];
export type OccurrenceWrite = Schemas["OccurrenceWrite"];
export type AppointmentAttendeeStatus = Schemas["AppointmentAttendeeStatus"];
export type AppointmentAnswer = Schemas["AppointmentResponseWrite"]["status"];
export type AppointmentVisibility = Schemas["AppointmentVisibility"];
export type AppointmentTransparency = Schemas["AppointmentTransparency"];
export type CalendarProblem = Schemas["CalendarProblem"];

/** The refusal code of a PUT whose If-Match revision has moved on. */
export const REVISION_MISMATCH = "revision_mismatch";

const appointmentPath = (id: number) => `/api/v1/calendar/appointments/${id}`;

/** One appointment, to its owner or one of its attendees; anyone else gets a 404. */
export const appointmentQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "appointments", id],
    queryFn: ({ signal }) => request<Appointment>(appointmentPath(id), { signal }),
  });

export const createAppointment = (input: AppointmentWrite): Promise<Appointment> =>
  request<Appointment>("/api/v1/calendar/appointments", json("POST", input));

/** A whole replace, at the revision it was read at. */
export const updateAppointment = (id: number, revision: number, input: AppointmentWrite): Promise<Appointment> =>
  request<Appointment>(appointmentPath(id), json("PUT", input, { "If-Match": String(revision) }));

/** The series with its attendees and exceptions. */
export const deleteAppointment = (id: number): Promise<void> =>
  request<void>(appointmentPath(id), { method: "DELETE" });

/** Moves or cancels one occurrence of a series, named by the day the series put it on. */
export const putOccurrence = (id: number, date: string, input: OccurrenceWrite): Promise<AppointmentOccurrence> =>
  request<AppointmentOccurrence>(`${appointmentPath(id)}/occurrences/${date}`, json("PUT", input));

/** Removes an occurrence's exception: the series' own occurrence returns. */
export const deleteOccurrence = (id: number, date: string): Promise<void> =>
  request<void>(`${appointmentPath(id)}/occurrences/${date}`, { method: "DELETE" });

/** An attendee's answer. */
export const respondToAppointment = (id: number, status: AppointmentAnswer): Promise<Appointment> =>
  request<Appointment>(`${appointmentPath(id)}/response`, json("POST", { status }));

/**
 * The whole write that puts an appointment back as it is, at a new span —
 * what a drag on the grid sends, since a PUT replaces every field. The zone
 * stays the one it was made in: it is what a series expands in and what was
 * agreed, and the mover's own zone changes nothing about either (design D2).
 */
export const appointmentWriteOf = (
  appointment: Appointment,
  span: { allDay: true; startDate: string; endDate: string } | { allDay: false; startsAt: string; endsAt: string },
): AppointmentWrite => ({
  title: appointment.title,
  allDay: span.allDay,
  ...(span.allDay
    ? { startDate: span.startDate, endDate: span.endDate }
    : { startsAt: span.startsAt, endsAt: span.endsAt }),
  timeZone: appointment.timeZone,
  visibility: appointment.visibility,
  transparency: appointment.transparency,
  ...(appointment.location ? { location: appointment.location } : {}),
  ...(appointment.description ? { description: appointment.description } : {}),
  ...(appointment.recurrence ? { recurrence: { rule: appointment.recurrence.rule } } : {}),
  attendees: appointment.attendees.map((attendee) => attendee.user.id),
  ...(appointment.customer ? { customerId: appointment.customer.id } : {}),
  ...(appointment.project ? { projectId: appointment.project.id } : {}),
});
