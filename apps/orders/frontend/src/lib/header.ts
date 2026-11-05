import type { Address, Order, Quote, Subscription } from "../api/types";

/** The header a quote and an order share (O13), as the editor holds it. */
export interface HeaderState {
  customerId: number;
  deliveryAddressId: number | null;
  contactId: number | null;
  yourReference: string;
  /** Sent only when the person changed it: the server keeps the salesperson otherwise. */
  salespersonId: string | null;
  salespersonChanged: boolean;
  projectId: number | null;
  currency: string;
  pricesIncludeVat: boolean;
  paymentTermsDays: number | "";
  discountVisible: boolean;
  language: "nb" | "en";
  introText: string;
  closingText: string;
  internalNote: string;
  deliveryDate: string | null;
  /** A quote's only (O20). */
  validUntil: string | null;
  /** An order's only: the Inventory site it is fulfilled from (O46). */
  siteId: number | null;
}

/** A quote's, an order's or a subscription's header as the editor holds it; a subscription has no delivery date. */
export const headerOf = (document: Quote | Order | Subscription): HeaderState => ({
  customerId: document.customer.customerId,
  deliveryAddressId: document.customer.deliveryAddressId ?? null,
  contactId: document.customer.contactId ?? null,
  yourReference: document.yourReference,
  salespersonId: document.salesperson?.userId ?? null,
  salespersonChanged: false,
  projectId: document.projectId ?? null,
  currency: document.currency,
  pricesIncludeVat: document.pricesIncludeVat,
  paymentTermsDays: document.paymentTermsDays,
  discountVisible: document.discountVisible,
  language: document.language,
  introText: document.introText,
  closingText: document.closingText,
  internalNote: document.internalNote,
  deliveryDate: "deliveryDate" in document ? (document.deliveryDate ?? null) : null,
  validUntil: "validUntil" in document ? (document.validUntil ?? null) : null,
  siteId: "siteId" in document ? (document.siteId ?? null) : null,
});

/** The header fields of a save (OrdersQuoteUpdate, OrdersOrderUpdate) — every one but the version and the lines. */
export const headerInput = (header: HeaderState) => ({
  customerId: header.customerId,
  deliveryAddressId: header.deliveryAddressId,
  contactId: header.contactId,
  yourReference: header.yourReference,
  ...(header.salespersonChanged ? { salespersonId: header.salespersonId } : {}),
  projectId: header.projectId,
  currency: header.currency,
  pricesIncludeVat: header.pricesIncludeVat,
  paymentTermsDays: header.paymentTermsDays === "" ? 0 : header.paymentTermsDays,
  discountVisible: header.discountVisible,
  language: header.language,
  introText: header.introText,
  closingText: header.closingText,
  internalNote: header.internalNote,
  deliveryDate: header.deliveryDate,
});

/** The currencies the picker offers; the document's own is added when it is another. */
export const CURRENCIES = ["NOK", "EUR", "SEK", "DKK", "USD", "GBP"] as const;

/** An address on one line, as the pickers and the read-only header show it. */
export const addressLine = (address?: Address | null) =>
  [address?.name, address?.line1, address?.line2, [address?.postalCode, address?.city].filter(Boolean).join(" ")]
    .filter((part) => part && String(part).trim() !== "")
    .join(", ");

/** A subscription's header fields of a save (OrdersSubscriptionUpdate): a quote's and an order's, but the delivery date. */
export const subscriptionHeaderInput = (header: HeaderState) => {
  const fields: Partial<ReturnType<typeof headerInput>> = headerInput(header);
  delete fields.deliveryDate;
  return fields as Omit<ReturnType<typeof headerInput>, "deliveryDate">;
};
