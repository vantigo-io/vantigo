import type { components } from "../api-schema";

type Schemas = components["schemas"];

/** The contract's shapes under the names the pages use (openapi/orders.yaml). */
export type Quote = Schemas["OrdersQuote"];
export type QuoteSummary = Schemas["OrdersQuoteSummary"];
export type QuotePage = Schemas["OrdersQuotePage"];
export type QuoteInput = Schemas["OrdersQuoteInput"];
export type QuoteUpdate = Schemas["OrdersQuoteUpdate"];
export type QuoteStatus = Quote["status"];
export type Accepted = Schemas["OrdersAccepted"];
export type AcceptInput = Schemas["OrdersAcceptInput"];

export type Order = Schemas["OrdersOrder"];
export type OrderSummary = Schemas["OrdersOrderSummary"];
export type OrderPage = Schemas["OrdersOrderPage"];
export type OrderInput = Schemas["OrdersOrderInput"];
export type OrderUpdate = Schemas["OrdersOrderUpdate"];
export type OrderStatus = Order["status"];
export type DeliveryStatus = Order["deliveryStatus"];
export type InvoiceStatus = Order["invoiceStatus"];
export type Change = Schemas["OrdersChange"];
export type ChangeAction = Schemas["OrdersChangeAction"];
export type DeliveredLine = Schemas["OrdersDeliveredLine"];

export type Line = Schemas["OrdersLine"];
export type LineInput = Schemas["OrdersLineInput"];
export type LineKind = Line["kind"];
export type Totals = Schemas["OrdersTotals"];
export type CustomerSnapshot = Schemas["OrdersCustomerSnapshot"];
export type Address = Schemas["OrdersAddress"];
export type Person = Schemas["OrdersPerson"];
export type SendInput = Schemas["OrdersSendInput"];
export type Mailing = Schemas["OrdersMailing"];
export type StoredDocument = Schemas["OrdersStoredDocument"];

export type Template = Schemas["OrdersTemplate"];
export type TemplateSummary = Schemas["OrdersTemplateSummary"];
export type TemplateInput = Schemas["OrdersTemplateInput"];
export type TemplateLine = Schemas["OrdersTemplateLine"];
export type TemplateApplied = Schemas["OrdersTemplateApplied"];

export type Settings = Schemas["OrdersSettings"];
export type SettingsInput = Schemas["OrdersSettingsInput"];
export type MailText = Schemas["OrdersMailText"];

export type SendChannel = Schemas["OrdersSendChannel"];
export type Meta = Schemas["OrdersMeta"];
export type Salesperson = Schemas["OrdersSalesperson"];
export type Variant = Schemas["OrdersVariant"];

export type Warning = Schemas["OrdersWarning"];
export type WarningInput = Schemas["OrdersWarningInput"];
export type WarningScope = Warning["scope"];
export type WarningLevel = Warning["level"];
export type ConfirmInput = Schemas["OrdersConfirmInput"];

export type VariantGrid = Schemas["OrdersVariantGrid"];
export type GridVariant = Schemas["OrdersGridVariant"];
export type VariantPackaging = Schemas["OrdersVariantPackaging"];

export type HitRate = Schemas["OrdersHitRate"];
export type HitRateRow = Schemas["OrdersHitRateRow"];
export type Intake = Schemas["OrdersIntake"];
export type IntakeRow = Schemas["OrdersIntakeRow"];
export type IntakeGroupBy = Intake["groupBy"];
export type Backlog = Schemas["OrdersBacklog"];
export type BacklogAged = Schemas["OrdersBacklogAged"];

export type CustomerSummary = Schemas["OrdersCustomerSummary"];
export type CurrencySum = Schemas["OrdersCurrencySum"];

/** The header fields a quote and an order share (O13), as the editor holds them. */
export type DocumentKind = "quote" | "order";
