import type { ContactResponse } from "../api/contacts";

/**
 * Composes a contact's display name from its raw name parts in Western order:
 * "prefix first middle last suffix", skipping the parts that are not set.
 */
export const formatContactName = (
  contact: Pick<ContactResponse, "firstName" | "lastName" | "middleName" | "prefix" | "suffix">,
): string =>
  [contact.prefix, contact.firstName, contact.middleName, contact.lastName, contact.suffix].filter(Boolean).join(" ");
