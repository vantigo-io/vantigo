import type { Line, Order, Quote, Totals } from "../api/types";

export const totals = (overrides: Partial<Totals> = {}): Totals => ({
  net: 1500,
  vat: 375,
  gross: 1875,
  vatSummaries: [{ category: "S", ratePercent: 25, taxableAmount: 1500, vatAmount: 375 }],
  ...overrides,
});

export const line = (overrides: Partial<Line> = {}): Line => ({
  id: 5001,
  position: 1,
  kind: "product",
  variantId: 301,
  sku: "KAB-10",
  name: "Kabel 10 m",
  description: "Skjøtekabel",
  quantity: 3,
  unit: "stk",
  unitPrice: 500,
  displayUnitPrice: 500,
  discountPercent: 0,
  lineGross: 1500,
  lineAllowance: 0,
  lineNet: 1500,
  priceSource: "catalog",
  vatCode: "3",
  vatCodeId: 3,
  vatRatePercent: 25,
  vatCategory: "S",
  invoicePolicy: "order",
  optional: false,
  qtyCancelled: 0,
  qtyDelivered: 0,
  qtyInvoiced: 0,
  deliveryMethod: "manual",
  ...overrides,
});

const customer = {
  customerId: 42,
  customerNumber: 10001,
  name: "Acme AS",
  type: "business" as const,
  invoiceAddress: { name: "Acme AS", line1: "Storgata 1", postalCode: "0155", city: "Oslo", country: "NO" },
  deliveryAddress: { name: "Acme lager", line1: "Lagerveien 2", postalCode: "2000", city: "Lillestrøm", country: "NO" },
  deliveryAddressId: 7,
  contact: { name: "Ola Kunde", email: "ola@acme.no", phone: null },
  contactId: 9,
  invoiceEmail: "faktura@acme.no",
};

const header = {
  customer,
  defaultRecipient: "ola@acme.no",
  yourReference: "PO-77",
  currency: "NOK",
  pricesIncludeVat: false,
  paymentTermsDays: 14,
  discountVisible: true,
  language: "nb" as const,
  introText: "",
  closingText: "",
  internalNote: "",
  deliveryDate: null,
  projectId: null,
  salesperson: { displayName: "Kari Selger", userId: "00000000-0000-4000-8000-000000000001" },
  totals: totals(),
  createdAt: "2026-11-01T08:00:00Z",
  updatedAt: "2026-11-01T08:00:00Z",
};

export const quote = (overrides: Partial<Quote> = {}): Quote => ({
  id: 1001,
  number: 1042,
  status: "draft",
  revision: 1,
  validUntil: "2026-12-01",
  origin: { channel: "manual" },
  lines: [line()],
  ...header,
  ...overrides,
});

export const order = (overrides: Partial<Order> = {}): Order => ({
  id: 2001,
  number: 5001,
  status: "draft",
  onHold: false,
  locked: false,
  version: 1,
  deliveryStatus: "none",
  invoiceStatus: "none",
  done: false,
  origin: { channel: "manual" },
  lines: [line()],
  ...header,
  ...overrides,
});

/** What GET /meta answers: sending on, a quote valid 30 days. */
export const meta = (overrides: Record<string, unknown> = {}) => ({
  sendingAvailable: true,
  quoteValidityDays: 30,
  defaultPaymentTermsDays: 14,
  baseCurrency: "NOK",
  ...overrides,
});

export const settings = (overrides: Record<string, unknown> = {}) => ({
  quoteSeriesStart: 1001,
  orderSeriesStart: 5001,
  nextQuoteNumber: 1043,
  nextOrderNumber: 5002,
  quoteValidityDays: 30,
  defaultPaymentTermsDays: 14,
  confirmationOnConfirm: false,
  combineByDefault: true,
  mailTexts: [],
  sendingAvailable: true,
  sendChannelId: null,
  sendChannelState: "disabled",
  sendChannels: [
    {
      id: "11111111-1111-4111-8111-111111111111",
      address: "salg@firma.no",
      displayName: "Salg",
      provider: "smtp",
      providerDisplayName: "SMTP",
      available: true,
    },
  ],
  revision: 3,
  ...overrides,
});
