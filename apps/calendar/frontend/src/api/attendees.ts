import type { Appointment } from "./appointments";
import { request } from "./request";

/**
 * Sends an outside attendee's invitation again, with the same link (design
 * D3, D5): the owner only. Answers the appointment with the attendee's
 * invitation as the send left it.
 */
export const resendInvitation = (appointmentId: number, attendeeId: number): Promise<Appointment> =>
  request<Appointment>(`/api/v1/calendar/appointments/${appointmentId}/attendees/${attendeeId}/resend`, {
    method: "POST",
  });
