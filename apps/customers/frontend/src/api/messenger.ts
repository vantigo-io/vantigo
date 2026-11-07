import { request } from "./request";

/**
 * What a customer's contact row asks of Communications (Messenger design N21, N22): a contact's
 * Messenger link for a channel, a first message written to a linked person, and the link undone.
 * Communications checks each against its own permissions; this package never fetches them.
 */

/** The Messenger link of a contact for a messenger channel: send it to that contact only. */
export const createMessengerLink = (channelId: string, contactId: number) =>
  request<{ url: string; expiresAt: string }>(
    `/api/v1/communications/channels/${encodeURIComponent(channelId)}/contact-links`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ contactId }),
    },
  );

/**
 * Opens the person's open conversation on the Page, or a new one, with textBody as its first message. The
 * server refuses with 422 reply_window_closed once the person's window is over.
 */
export const writeOnMessenger = (participantId: string, textBody: string, idempotencyKey: string) =>
  request<{ conversationId: string; messageId: string | null; status: string }>(
    "/api/v1/communications/conversations",
    {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": idempotencyKey },
      body: JSON.stringify({ participantId, textBody }),
    },
  );

export const unlinkMessengerParticipant = (participantId: string) =>
  request<void>(`/api/v1/communications/participants/${encodeURIComponent(participantId)}/contact`, {
    method: "DELETE",
  });
