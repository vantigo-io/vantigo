import type {
  PurchaseOrder,
  PurchaseOrderLine,
  PurchaseOrderSummary,
  PurchasingMeta,
  PurchasingSupplier,
  PurchasingSupplierItem,
  PurchasingVariantOption,
} from "../api/purchasing";
import { type Call, jsonResponse } from "./api";

const STAMP = "2026-11-16T09:00:00Z";

export const meta = (overrides: Partial<PurchasingMeta> = {}): PurchasingMeta => ({
  sendingAvailable: true,
  baseCurrency: "NOK",
  priceTolerancePercent: "2",
  canManageSuppliers: true,
  canOpenAccountingSuppliers: true,
  defaultSiteId: 1,
  ...overrides,
});

export const line = (overrides: Partial<PurchaseOrderLine> = {}): PurchaseOrderLine => ({
  id: 11,
  position: 1,
  kind: "variant",
  variantId: 302,
  sku: "SKR-10",
  name: "Skrue 10 mm",
  description: "",
  unit: "stk",
  productType: "Goods",
  orderQuantity: "10",
  quantity: "1000",
  packagingId: 5,
  packagingName: "Eske",
  packagingQuantity: "100",
  unitPrice: "45.50",
  discountPercent: "0",
  lineNet: "455.00",
  received: "0",
  invoiced: "0",
  open: "1000",
  expectedOn: "2026-11-20",
  supplierItemNumber: "LEV-77",
  warnings: [],
  closedAt: null,
  ...overrides,
});

export const order = (overrides: Partial<PurchaseOrder> = {}): PurchaseOrder => ({
  id: 501,
  number: 3001,
  status: "sent",
  version: 1,
  supplierId: 40,
  supplierName: "Skruer AS",
  supplierNumber: 2001,
  supplierAddress: { line1: "Verkstedveien 1", postalCode: "0150", city: "Oslo", country: "NO" },
  supplierContact: "Per",
  supplierEmail: "ordre@skruer.no",
  supplierActive: true,
  siteId: 1,
  siteName: "Hovedlager",
  deliveryAddress: { name: "Hovedlager", line1: "Lagerveien 2", postalCode: "2000", city: "Lillestrøm" },
  currency: "NOK",
  language: "nb",
  orderDate: "2026-11-10",
  expectedOn: "2026-11-20",
  note: "",
  internalNote: "",
  netTotal: "455.00",
  late: false,
  unconfirmed: false,
  sendMethod: "mail",
  sentAt: STAMP,
  sentBy: { id: "00000000-0000-4000-8000-000000000001", displayName: "Kari Innkjøper" },
  buyer: { id: "00000000-0000-4000-8000-000000000001", displayName: "Kari Innkjøper" },
  createdAt: STAMP,
  updatedAt: STAMP,
  lines: [line()],
  ...overrides,
});

export const draft = (overrides: Partial<PurchaseOrder> = {}): PurchaseOrder =>
  order({ number: null, status: "draft", version: 0, sentAt: null, sentBy: null, sendMethod: null, ...overrides });

export const summary = (overrides: Partial<PurchaseOrderSummary> = {}): PurchaseOrderSummary => ({
  id: 501,
  number: 3001,
  status: "sent",
  supplierId: 40,
  supplierName: "Skruer AS",
  siteId: 1,
  currency: "NOK",
  netTotal: "455.00",
  orderDate: "2026-11-10",
  expectedOn: "2026-11-20",
  late: false,
  unconfirmed: false,
  goodsLines: 4,
  receivedLines: 1,
  invoicedLines: 0,
  lineCount: 5,
  updatedAt: STAMP,
  ...overrides,
});

export const supplier = (overrides: Partial<PurchasingSupplier> = {}): PurchasingSupplier => ({
  id: 40,
  number: 2001,
  name: "Skruer AS",
  organisationNumber: "912345678",
  country: "NO",
  addressLine: "Verkstedveien 1",
  postalCode: "0150",
  city: "Oslo",
  email: "ordre@skruer.no",
  currency: "NOK",
  paymentTermsDays: 30,
  active: true,
  isPerson: false,
  anonymised: false,
  supplierItemCount: 1,
  openOrderCount: 2,
  ...overrides,
});

export const supplierItem = (overrides: Partial<PurchasingSupplierItem> = {}): PurchasingSupplierItem => ({
  id: 70,
  supplierId: 40,
  supplierName: "Skruer AS",
  variantId: 302,
  productId: 30,
  sku: "SKR-10",
  productName: "Skrue 10 mm",
  unit: "stk",
  productStatus: "Active",
  currency: "NOK",
  currencyDiffers: false,
  packagingMissing: false,
  packagingId: 5,
  packagingName: "Eske",
  packagingQuantity: "100",
  price: "45.5",
  priceSetBy: "person",
  priceSetOn: "2026-11-01",
  leadTimeDays: 5,
  minimumQuantity: "10",
  multiple: "5",
  preferred: true,
  supplierItemNumber: "LEV-77",
  supplierItemName: null,
  revision: 3,
  updatedAt: STAMP,
  ...overrides,
});

export const variantOption = (overrides: Partial<PurchasingVariantOption> = {}): PurchasingVariantOption => ({
  variantId: 302,
  productId: 30,
  sku: "SKR-10",
  name: "Skrue 10 mm",
  unit: "stk",
  productType: "Goods",
  productStatus: "Active",
  packagings: [{ id: 5, name: "Eske", quantity: "100" }],
  supplierItem: supplierItem(),
  ...overrides,
});

/** What every page reads beside its own data, answered unless the test says otherwise. */
export const commonAnswers = (call: Call, metaOverrides: Partial<PurchasingMeta> = {}): Response | undefined => {
  if (call.method !== "GET") return undefined;
  if (call.url === "/api/v1/purchasing/meta") return jsonResponse(200, meta(metaOverrides));
  if (call.url === "/api/v1/purchasing/sites")
    return jsonResponse(200, {
      items: [
        { id: 1, code: "HL", name: "Hovedlager", active: true },
        { id: 2, code: "BL", name: "Butikklager", active: true },
      ],
    });
  if (call.url === "/api/v1/purchasing/buyers")
    return jsonResponse(200, {
      items: [{ id: "00000000-0000-4000-8000-000000000001", displayName: "Kari Innkjøper" }],
    });
  if (/\/history$/.test(call.url)) return jsonResponse(200, { changes: [], documents: [], mailings: [] });
  if (call.url.startsWith("/api/v1/purchasing/suppliers?"))
    return jsonResponse(200, { items: [supplier(), supplier({ id: 41, number: 2002, name: "Bolter AS" })] });
  return undefined;
};
