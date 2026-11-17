import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PURCHASING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

/** The contract's shapes under the names the pages use (openapi/purchasing.yaml). */
export type PurchasingMeta = Schemas["PurchasingMeta"];
export type PurchasingSummary = Schemas["PurchasingSummary"];
export type PurchaseOrder = Schemas["PurchasingOrder"];
export type PurchaseOrderLine = Schemas["PurchasingOrderLine"];
export type PurchaseOrderSummary = Schemas["PurchasingOrderSummary"];
export type PurchaseOrderPage = Schemas["PurchasingOrderPage"];
export type PurchaseOrderInput = Schemas["PurchasingOrderInput"];
export type PurchaseOrderUpdate = Schemas["PurchasingOrderUpdate"];
export type PurchaseOrderStatus = PurchaseOrder["status"];
export type PurchaseLineInput = Schemas["PurchasingLineInput"];
export type PurchaseLineWarning = Schemas["PurchasingLineWarning"];
export type PurchaseOrderHistory = Schemas["PurchasingOrderHistory"];
export type PurchasingChange = Schemas["PurchasingChange"];
export type PurchasingDocument = Schemas["PurchasingDocument"];
export type PurchasingMailing = Schemas["PurchasingMailing"];
export type PurchasingSendInput = Schemas["PurchasingSendInput"];
export type PurchasingSendAgainInput = Schemas["PurchasingSendAgainInput"];
export type PurchasingCancelInput = Schemas["PurchasingCancelInput"];
export type PurchasingConfirmationInput = Schemas["PurchasingConfirmationInput"];
export type PurchasingSendResult = Schemas["PurchasingSendResult"];
export type PurchasingAddress = Schemas["PurchasingAddress"];
export type PurchasingPerson = Schemas["PurchasingPerson"];
export type PurchasingSupplier = Schemas["PurchasingSupplier"];
export type PurchasingSupplierItem = Schemas["PurchasingSupplierItem"];
export type PurchasingSupplierItemInput = Schemas["PurchasingSupplierItemInput"];
export type PurchasingSupplierItemUpdate = Schemas["PurchasingSupplierItemUpdate"];
export type PurchasingSupplierItemPage = Schemas["PurchasingSupplierItemPage"];
export type PurchasingImportResult = Schemas["PurchasingImportResult"];
export type PurchasingImportRow = Schemas["PurchasingImportRow"];
export type PurchasingVariantOption = Schemas["PurchasingVariantOption"];
export type PurchasingPackaging = Schemas["PurchasingPackaging"];
export type PurchasingSite = Schemas["PurchasingSite"];
export type PurchasingSettings = Schemas["PurchasingSettings"];
export type PurchasingSettingsInput = Schemas["PurchasingSettingsInput"];
export type PurchasingSiteAddress = Schemas["PurchasingSiteAddress"];
export type PurchasingReceiveForm = Schemas["PurchasingReceiveForm"];
export type PurchasingReceiveLine = Schemas["PurchasingReceiveLine"];
export type PurchasingLocation = Schemas["PurchasingLocation"];
export type PurchasingReceiveCode = Schemas["PurchasingReceiveCode"];
export type PurchasingReceipt = Schemas["PurchasingReceipt"];
export type PurchasingReceiptLine = Schemas["PurchasingReceiptLine"];
export type PurchasingReceiptInput = Schemas["PurchasingReceiptInput"];
export type PurchasingReceiptLineInput = Schemas["PurchasingReceiptLineInput"];
export type PurchasingReceiptLotInput = Schemas["PurchasingReceiptLotInput"];
export type PurchasingReceiptResult = Schemas["PurchasingReceiptResult"];
export type PurchasingDeliveredResult = Schemas["PurchasingDeliveredResult"];
export type PurchasingOrderProgress = Schemas["PurchasingOrderProgress"];
export type PurchasingReceivingOrder = Schemas["PurchasingReceivingOrder"];

const BASE = "/api/v1/purchasing";

/** What the screens need without purchasing:settings: sending, the base currency, the supplier rights (P21, A12). */
export const purchasingMetaQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<PurchasingMeta>(`${BASE}/meta`, { signal }),
    staleTime: 60_000,
  });

/** The dashboard's figures (P23). */
export const purchasingSummaryQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "summary"],
    queryFn: ({ signal }) => request<PurchasingSummary>(`${BASE}/summary`, { signal }),
  });

export const getPurchasingSummary = (signal?: AbortSignal) => request<PurchasingSummary>(`${BASE}/summary`, { signal });

/** The list's filters (P23); a status filter is repeated, one parameter per status. */
export interface PurchaseOrderFilters {
  status?: readonly PurchaseOrderStatus[];
  supplierId?: number;
  late?: boolean;
  mine?: boolean;
  unconfirmed?: boolean;
  open?: boolean;
  q?: string;
  page?: number;
  pageSize?: number;
}

export const PAGE_SIZE = 25;

export const orderListQuery = (filters: PurchaseOrderFilters): string => {
  const search = new URLSearchParams();
  for (const status of filters.status ?? []) search.append("status", status);
  if (filters.supplierId !== undefined) search.set("supplierId", String(filters.supplierId));
  if (filters.late) search.set("late", "true");
  if (filters.mine) search.set("mine", "true");
  if (filters.unconfirmed) search.set("unconfirmed", "true");
  if (filters.open) search.set("open", "true");
  if (filters.q?.trim()) search.set("q", filters.q.trim());
  if (filters.page !== undefined && filters.page > 1) search.set("page", String(filters.page));
  if (filters.pageSize !== undefined) search.set("pageSize", String(filters.pageSize));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/** A page of purchase orders, newest first; the shown page stays while the next is fetched. */
export const purchaseOrdersQueryOptions = (filters: PurchaseOrderFilters) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "list", filters],
    queryFn: ({ signal }) => request<PurchaseOrderPage>(`${BASE}/orders${orderListQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

/** The spotlight's and other modules' search: orders by number, supplier and the supplier's reference. */
export const listPurchasingOrders = (filters: PurchaseOrderFilters, signal?: AbortSignal) =>
  request<PurchaseOrderPage>(`${BASE}/orders${orderListQuery(filters)}`, { signal });

export const purchaseOrderQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "detail", id],
    queryFn: ({ signal }) => request<PurchaseOrder>(`${BASE}/orders/${id}`, { signal }),
  });

export const purchaseOrderHistoryQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "history", id],
    queryFn: ({ signal }) => request<PurchaseOrderHistory>(`${BASE}/orders/${id}/history`, { signal }),
  });

export const createPurchaseOrder = (input: PurchaseOrderInput) =>
  send<PurchaseOrder>(`${BASE}/orders`, json("POST", input));
export const updatePurchaseOrder = (id: number, input: PurchaseOrderUpdate) =>
  send<PurchaseOrder>(`${BASE}/orders/${id}`, json("PUT", input));
/** Deletes a draft as it was read (409 `version_conflict` otherwise). */
export const deletePurchaseOrder = (id: number, expectedUpdatedAt: string) =>
  send<void>(`${BASE}/orders/${id}${queryString({ expectedUpdatedAt })}`, { method: "DELETE" });

export const sendPurchaseOrder = (id: number, input: PurchasingSendInput) =>
  send<PurchasingSendResult>(`${BASE}/orders/${id}/send`, json("POST", input));
export const markPurchaseOrderSent = (id: number, expectedUpdatedAt: string, clientRequestId: string) =>
  send<PurchasingSendResult>(`${BASE}/orders/${id}/mark-sent`, json("POST", { expectedUpdatedAt, clientRequestId }));
export const sendPurchaseOrderAgain = (id: number, input: PurchasingSendAgainInput) =>
  send<PurchasingSendResult>(`${BASE}/orders/${id}/send-again`, json("POST", input));
export const cancelPurchaseOrder = (id: number, input: PurchasingCancelInput) =>
  send<PurchasingSendResult>(`${BASE}/orders/${id}/cancel`, json("POST", input));
export const closePurchaseOrder = (id: number, expectedUpdatedAt: string) =>
  send<PurchaseOrder>(`${BASE}/orders/${id}/close`, json("POST", { expectedUpdatedAt }));
export const reopenPurchaseOrder = (id: number, expectedUpdatedAt: string) =>
  send<PurchaseOrder>(`${BASE}/orders/${id}/reopen`, json("POST", { expectedUpdatedAt }));
export const closePurchaseOrderLine = (id: number, lineId: number, expectedUpdatedAt: string) =>
  send<PurchaseOrder>(`${BASE}/orders/${id}/lines/${lineId}/close`, json("POST", { expectedUpdatedAt }));
export const confirmPurchaseOrder = (id: number, input: PurchasingConfirmationInput) =>
  send<PurchaseOrder>(`${BASE}/orders/${id}/confirmation`, json("PUT", input));
export const copyPurchaseOrder = (id: number) => send<PurchaseOrder>(`${BASE}/orders/${id}/copy`, { method: "POST" });

/** What the receive screen needs (P24, A21): the open goods lines, their remainders and the site's locations — no prices. */
export const receiveFormQueryOptions = (orderId: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "receive", orderId],
    queryFn: ({ signal }) => request<PurchasingReceiveForm>(`${BASE}/orders/${orderId}/receive`, { signal }),
  });

/** The order's open goods lines a scanned code names (P30), each with what one scan adds. */
export const lookupReceiveCode = (orderId: number, code: string, signal?: AbortSignal) =>
  request<{ items: PurchasingReceiveCode[] }>(`${BASE}/orders/${orderId}/receive/code${queryString({ code })}`, {
    signal,
  });

/** The orders to receive (A47): sent and partially received orders with goods still to come — no prices. */
export const ordersToReceiveQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "receiving"],
    queryFn: ({ signal }) => request<{ items: PurchasingReceivingOrder[] }>(`${BASE}/receiving`, { signal }),
  });

/** One receipt, idempotent by its clientRequestId (A11); 409 `over_receipt` unless acceptOver. */
export const receivePurchaseOrder = (orderId: number, input: PurchasingReceiptInput) =>
  send<PurchasingReceiptResult>(`${BASE}/orders/${orderId}/receipts`, json("POST", input));

/** The order's receipts, newest first (P28). */
export const purchaseOrderReceiptsQueryOptions = (orderId: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "receipts", orderId],
    queryFn: ({ signal }) => request<{ items: PurchasingReceipt[] }>(`${BASE}/orders/${orderId}/receipts`, { signal }),
  });

export const purchasingReceiptQueryOptions = (receiptId: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "receipts", "detail", receiptId],
    queryFn: ({ signal }) => request<PurchasingReceipt>(`${BASE}/receipts/${receiptId}`, { signal }),
  });

/** Takes a receipt back (P29), with an optional note the reversal carries. */
export const undoPurchasingReceipt = (receiptId: number, note?: string) =>
  send<PurchasingReceiptResult>(`${BASE}/receipts/${receiptId}/undo`, json("POST", note ? { note } : {}));

/** Marks a text or service line delivered, or takes the mark off (P16), on the order as read. */
export const markPurchaseOrderLineDelivered = (
  orderId: number,
  lineId: number,
  delivered: boolean,
  expectedUpdatedAt: string,
) =>
  send<PurchasingDeliveredResult>(
    `${BASE}/orders/${orderId}/lines/${lineId}/delivered`,
    json("POST", { delivered, expectedUpdatedAt }),
  );

/** The order's PDF printed now (a draft's preview), and a stored one as it was sent. */
export const purchaseOrderPdfUrl = (id: number) => `${BASE}/orders/${id}/pdf`;
export const purchaseOrderDocumentUrl = (id: number, documentId: number) =>
  `${BASE}/orders/${id}/documents/${documentId}`;

/** How many suppliers a search answers at most; a full page says so. */
export const SUPPLIER_LIST_LIMIT = 50;

/** The register's suppliers matching a search, inactive ones too (P13). */
export const purchasingSuppliersQueryOptions = (q?: string) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "suppliers", "list", q ?? ""],
    queryFn: ({ signal }) =>
      request<{ items: PurchasingSupplier[] }>(`${BASE}/suppliers${queryString({ q, limit: SUPPLIER_LIST_LIMIT })}`, {
        signal,
      }),
    placeholderData: keepPreviousData,
  });

export const purchasingSupplierQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "suppliers", "detail", id],
    queryFn: ({ signal }) => request<PurchasingSupplier>(`${BASE}/suppliers/${id}`, { signal }),
  });

/** The supplier items' filters: one supplier's, or those of given variants (the product page's card). */
export interface SupplierItemFilters {
  supplierId?: number;
  variantIds?: readonly number[];
  q?: string;
  page?: number;
  pageSize?: number;
}

const supplierItemQuery = (filters: SupplierItemFilters): string => {
  const search = new URLSearchParams();
  if (filters.supplierId !== undefined) search.set("supplierId", String(filters.supplierId));
  for (const id of filters.variantIds ?? []) search.append("variantId", String(id));
  if (filters.q?.trim()) search.set("q", filters.q.trim());
  if (filters.page !== undefined && filters.page > 1) search.set("page", String(filters.page));
  if (filters.pageSize !== undefined) search.set("pageSize", String(filters.pageSize));
  const text = search.toString();
  return text ? `?${text}` : "";
};

export const listPurchasingSupplierItems = (filters: SupplierItemFilters, signal?: AbortSignal) =>
  request<PurchasingSupplierItemPage>(`${BASE}/supplier-items${supplierItemQuery(filters)}`, { signal });

export const supplierItemsQueryOptions = (filters: SupplierItemFilters) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "supplier-items", "list", filters],
    queryFn: ({ signal }) => listPurchasingSupplierItems(filters, signal),
    placeholderData: keepPreviousData,
  });

export const createSupplierItem = (input: PurchasingSupplierItemInput) =>
  send<PurchasingSupplierItem>(`${BASE}/supplier-items`, json("POST", input));
export const updateSupplierItem = (id: number, input: PurchasingSupplierItemUpdate) =>
  send<PurchasingSupplierItem>(`${BASE}/supplier-items/${id}`, json("PUT", input));
export const deleteSupplierItem = (id: number) => send<void>(`${BASE}/supplier-items/${id}`, { method: "DELETE" });

/** A supplier's price list as CSV (P14): a dry run unless `commit`; a commit writes every row or none. */
export const importSupplierItems = (supplierId: number, csv: string, commit: boolean) =>
  send<PurchasingImportResult>(`${BASE}/supplier-items/import${queryString({ supplierId, dryRun: !commit })}`, {
    method: "POST",
    headers: { "Content-Type": "text/csv" },
    body: csv,
  });

/** Variants a line may name, with the supplier's item when the supplier sells it (P16). */
export const searchPurchasingVariants = (q: string, supplierId?: number, signal?: AbortSignal) =>
  request<{ items: PurchasingVariantOption[] }>(`${BASE}/variants${queryString({ q, supplierId, limit: 20 })}`, {
    signal,
  });

export const purchasingSitesQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "sites"],
    queryFn: ({ signal }) => request<{ items: PurchasingSite[] }>(`${BASE}/sites`, { signal }),
    staleTime: 60_000,
  });

export const purchasingBuyersQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "buyers"],
    queryFn: ({ signal }) => request<{ items: PurchasingPerson[] }>(`${BASE}/buyers`, { signal }),
    staleTime: 60_000,
  });

export const purchasingSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<PurchasingSettings>(`${BASE}/settings`, { signal }),
  });

export const savePurchasingSettings = (input: PurchasingSettingsInput) =>
  send<PurchasingSettings>(`${BASE}/settings`, json("PUT", input));

/** The invoice match (purchasing design P36–P41, A20): the contract's shapes. */
export type PurchasingInvoicePanel = Schemas["PurchasingInvoicePanel"];
export type PurchasingInvoiceMatch = Schemas["PurchasingInvoiceMatch"];
export type PurchasingInvoiceLine = Schemas["PurchasingInvoiceLine"];
export type PurchasingInvoiceSuggestion = Schemas["PurchasingInvoiceSuggestion"];
export type PurchasingInvoiceWarning = Schemas["PurchasingInvoiceWarning"];
export type PurchasingInvoiceMatchInput = Schemas["PurchasingInvoiceMatchInput"];
export type PurchasingInvoiceMatchLineInput = Schemas["PurchasingInvoiceMatchLineInput"];
export type PurchasingInvoiceUnmatchInput = Schemas["PurchasingInvoiceUnmatchInput"];
export type PurchasingInvoiceOrderMatches = Schemas["PurchasingInvoiceOrderMatches"];
export type PurchasingInvoiceRegisterInput = Schemas["PurchasingInvoiceRegisterInput"];
export type PurchasingInvoiceRegistration = Schemas["PurchasingInvoiceRegistration"];
export type PurchasingInvoiceUninvoiced = Schemas["PurchasingInvoiceUninvoiced"];
export type PurchasingInvoiceUninvoicedRow = Schemas["PurchasingInvoiceUninvoicedRow"];

/** The supplier invoices matched to an order, provisional and posted (P37, P40). */
export const purchaseOrderInvoicesQueryOptions = (orderId: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "orders", "invoices", orderId],
    queryFn: ({ signal }) => request<PurchasingInvoiceOrderMatches>(`${BASE}/orders/${orderId}/invoices`, { signal }),
  });

/** Register invoice (P37, A1, A11): Accounting's draft from the order, idempotent by its clientRequestId. */
export const registerPurchaseOrderInvoice = (orderId: number, input: PurchasingInvoiceRegisterInput) =>
  send<PurchasingInvoiceRegistration>(`${BASE}/orders/${orderId}/register-invoice`, json("POST", input));

/** The Purchase order panel of a supplier invoice (P36–P38, A20). */
export const invoicePanelQueryOptions = (invoiceId: number) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "supplier-invoices", "match", invoiceId],
    queryFn: ({ signal }) =>
      request<PurchasingInvoicePanel>(`${BASE}/supplier-invoices/${invoiceId}/match`, { signal }),
  });

/** Allocates invoice lines to an order's lines; 409 `over_invoiced` unless acceptOver. */
export const matchPurchasingInvoice = (invoiceId: number, input: PurchasingInvoiceMatchInput) =>
  send<PurchasingInvoicePanel>(`${BASE}/supplier-invoices/${invoiceId}/matches`, json("POST", input));
/** Match all received of one order (P37). */
export const matchAllPurchasingInvoice = (invoiceId: number, orderId: number) =>
  send<PurchasingInvoicePanel>(`${BASE}/supplier-invoices/${invoiceId}/match-all`, json("POST", { orderId }));
/** Unmatches the invoice: everything, one order's, or one line's. */
export const unmatchPurchasingInvoice = (invoiceId: number, input: PurchasingInvoiceUnmatchInput) =>
  send<PurchasingInvoicePanel>(`${BASE}/supplier-invoices/${invoiceId}/unmatch`, json("POST", input));

/** Received, not invoiced (P41): as of a day, optionally one supplier's. */
export interface UninvoicedFilters {
  asOf?: string;
  supplierId?: number;
}

export const uninvoicedQueryOptions = (filters: UninvoicedFilters) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "received-not-invoiced", filters],
    queryFn: ({ signal }) =>
      request<PurchasingInvoiceUninvoiced>(`${BASE}/received-not-invoiced${queryString({ ...filters })}`, { signal }),
    placeholderData: keepPreviousData,
  });

/** The list as CSV, with the same filters. */
export const uninvoicedCsvUrl = (filters: UninvoicedFilters) =>
  `${BASE}/received-not-invoiced/csv${queryString({ ...filters })}`;
