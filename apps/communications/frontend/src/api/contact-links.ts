import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A Messenger participant's link to a contact of Customers (Messenger design N20, N21): the participant
 * the thread is with, the contact it is linked to, the one a shared e-mail address suggests, and the
 * one whose Messenger link it used while linked to another. Null for an e-mail conversation.
 */
export type MessengerLink = {
  participantId: string;
  contactId: number | null;
  suggestedContactId: number | null;
  referralContactId: number | null;
};

/** A contact as the thread's picker and header name it. */
export type ContactOption = { id: number; name: string };

type ContactBody = { id: number; firstName: string; lastName: string; middleName?: string | null };

const nameOf = (c: ContactBody): string =>
  [c.firstName, c.middleName, c.lastName]
    .filter((part) => part?.trim())
    .join(" ")
    .trim() || `#${c.id}`;

/**
 * The contacts of a customer, which a conversation linked to that customer may link a participant to.
 * Customers answers 403 to a caller who may not view its contacts; the picker is then empty and the
 * server remains the authority on what may be linked.
 */
export const customerContactOptionsQueryOptions = (customerId: number | null) =>
  queryOptions({
    queryKey: ["messenger-contact-options", customerId],
    queryFn: async ({ signal }): Promise<ContactOption[]> => {
      try {
        const body = await request<{ data: { contact: ContactBody }[] }>(`/api/v1/customers/${customerId}/contacts`, {
          signal,
          handleUnauthorized: false,
        });
        return body.data.map((row) => ({ id: row.contact.id, name: nameOf(row.contact) }));
      } catch (error) {
        if ((error as { status?: number }).status === 403) return [];
        throw error;
      }
    },
    enabled: customerId != null,
  });

/** One contact's name, null when Customers will not say (a caller without its permission, or a contact since deleted). */
export const contactNameQueryOptions = (contactId: number | null) =>
  queryOptions({
    queryKey: ["messenger-contact-name", contactId],
    queryFn: async ({ signal }): Promise<string | null> => {
      try {
        const body = await request<ContactBody>(`/api/v1/customers/contacts/${contactId}`, {
          signal,
          handleUnauthorized: false,
        });
        return nameOf(body);
      } catch (error) {
        const status = (error as { status?: number }).status;
        if (status === 403 || status === 404) return null;
        throw error;
      }
    },
    enabled: contactId != null,
  });

/** Links a participant to a contact; conversationId makes the server check the contact is its customer's. */
export const linkParticipantContact = (participantId: string, contactId: number, conversationId: string) =>
  request<{ participantId: string; contactId: number | null }>(
    `/api/v1/communications/participants/${encodeURIComponent(participantId)}/contact`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ contactId, conversationId }),
    },
  );

export const unlinkParticipantContact = (participantId: string) =>
  request<void>(`/api/v1/communications/participants/${encodeURIComponent(participantId)}/contact`, {
    method: "DELETE",
  });
