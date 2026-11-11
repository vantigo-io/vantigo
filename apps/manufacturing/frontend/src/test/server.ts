import type { ManufacturingBom, ManufacturingBomOverview, ManufacturingBomSummary } from "../api/boms";
import type { ManufacturingLocation } from "../api/locations";
import type { ManufacturingMeta } from "../api/meta";
import type {
  ManufacturingOrder,
  ManufacturingOrderEvent,
  ManufacturingOrderSummary,
  ManufacturingProductionDraft,
} from "../api/orders";
import type { ManufacturingPerson } from "../api/people";
import type { ManufacturingSite } from "../api/sites";
import type { ManufacturingStats } from "../api/stats";
import type { ManufacturingVariantSearchItem } from "../api/variants";
import type { ManufacturingWorkCenter } from "../api/work-centers";
import { defaultPattern } from "../lib/pattern";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

const STAMP = "2026-11-11T09:00:00Z";

export const sites = (): ManufacturingSite[] => [
  { id: 1, code: "OSL", name: "Oslo fabrikk", rootLocationId: 10 },
  { id: 2, code: "BGO", name: "Bergen verksted", rootLocationId: 20 },
];

export const variants = (): ManufacturingVariantSearchItem[] => [
  {
    id: 1,
    sku: "TABLE",
    productName: "Oak table",
    unit: "pcs",
    productType: "goods",
    productStatus: "active",
    hasBom: true,
  },
  {
    id: 2,
    sku: "BOARD",
    productName: "Oak board",
    unit: "pcs",
    productType: "goods",
    productStatus: "active",
    hasBom: false,
  },
  {
    id: 3,
    sku: "LEG",
    productName: "Table leg",
    unit: "pcs",
    productType: "goods",
    productStatus: "active",
    hasBom: true,
  },
];

export const workCenter = (id: number, code: string, patch: Partial<ManufacturingWorkCenter> = {}) =>
  ({
    id,
    code,
    name: `${code} station`,
    siteId: 1,
    siteCode: "OSL",
    siteName: "Oslo fabrikk",
    capacity: 1,
    timeEfficiency: 100,
    setupMinutes: "0",
    cleanupMinutes: "0",
    pattern: defaultPattern(),
    workingState: "normal",
    status: "active",
    revision: 1,
    createdAt: STAMP,
    updatedAt: STAMP,
    ...patch,
  }) satisfies ManufacturingWorkCenter;

/** A search item as a variant reference: the same fields but whether it has a BoM. */
const ref = (variant: ManufacturingVariantSearchItem) => ({
  id: variant.id,
  sku: variant.sku,
  productName: variant.productName,
  unit: variant.unit,
  productType: variant.productType,
  productStatus: variant.productStatus,
});

export const bomFixture = (patch: Partial<ManufacturingBom> = {}): ManufacturingBom => {
  const [table, board] = variants();
  return {
    id: 7,
    variant: ref(table),
    code: "Rev B",
    outputQuantity: "1",
    isDefault: true,
    makeToOrder: false,
    leadTimeDays: 0,
    note: "",
    status: "active",
    revision: 3,
    siteId: 1,
    createdAt: STAMP,
    updatedAt: STAMP,
    operations: [
      {
        id: 30,
        key: "30",
        sequence: 10,
        name: "Cut",
        workCenterId: 1,
        workCenterCode: "SAW",
        workCenterName: "SAW station",
        setupMinutes: null,
        minutesPerCycle: "4",
        instructions: "",
      },
    ],
    lines: [
      {
        id: 50,
        variant: ref(board),
        quantity: "4",
        scrapPercent: "0",
        operationId: 30,
        operationKey: "30",
        sortOrder: 10,
        note: "",
      },
    ],
    ...patch,
  };
};

export const summaryOf = (bom: ManufacturingBom): ManufacturingBomSummary => ({
  id: bom.id,
  variant: bom.variant,
  code: bom.code,
  outputQuantity: bom.outputQuantity,
  isDefault: bom.isDefault,
  makeToOrder: bom.makeToOrder,
  status: bom.status,
  revision: bom.revision,
  lineCount: bom.lines.length,
  operationCount: bom.operations.length,
  updatedAt: bom.updatedAt,
});

export const locations = (): ManufacturingLocation[] => [
  { id: 10, fullCode: "OSL", kind: "site", parentId: null, root: true, active: true },
  { id: 11, fullCode: "OSL-A1", kind: "shelf", parentId: 10, root: false, active: true },
  { id: 12, fullCode: "OSL-OLD", kind: "shelf", parentId: 10, root: false, active: false },
];

export const people = (): ManufacturingPerson[] => [
  { id: "7d1f2a51-9b0c-4c1e-8a4e-2f3b6c7d8e90", displayName: "Kari Nordmann" },
];

/** A confirmed order of ten tables, four boards a table, nothing produced yet. */
export const orderFixture = (patch: Partial<ManufacturingOrder> = {}): ManufacturingOrder => {
  const [table, board] = variants();
  return {
    id: 5,
    number: "MO-12",
    variant: ref(table),
    bomId: 7,
    bomCode: "Rev B",
    bomOutputQuantity: "1",
    quantity: "10",
    produced: "0",
    outputScrap: "0",
    overrun: "0",
    siteId: 1,
    componentLocationId: null,
    outputLocationId: 10,
    dueDate: "2026-11-20",
    plannedStart: null,
    plannedFinish: null,
    priority: "normal",
    responsible: null,
    note: "",
    origin: { kind: "manual", orderId: null, lineId: null, reference: null },
    status: "confirmed",
    late: false,
    availability: "ready",
    reservationStatus: "reserved",
    backorderOf: null,
    backorderOfId: null,
    backorders: [],
    parent: null,
    children: [],
    confirmedAt: STAMP,
    closedAt: null,
    revision: 4,
    createdAt: STAMP,
    updatedAt: STAMP,
    components: [
      {
        id: 60,
        variant: ref(board),
        required: "40",
        reserved: "40",
        available: "100",
        consumed: "0",
        scrapped: "0",
        remaining: "40",
        missing: "0",
        covered: true,
        added: false,
        operationId: 30,
        sortOrder: 10,
        note: "",
      },
    ],
    ...patch,
  };
};

export const orderSummaryOf = (order: ManufacturingOrder): ManufacturingOrderSummary => ({
  id: order.id,
  number: order.number,
  variant: order.variant,
  bomId: order.bomId,
  quantity: order.quantity,
  produced: order.produced,
  siteId: order.siteId,
  dueDate: order.dueDate,
  plannedStart: order.plannedStart,
  plannedFinish: order.plannedFinish,
  priority: order.priority,
  responsible: order.responsible,
  origin: order.origin,
  status: order.status,
  late: order.late,
  reservationStatus: order.reservationStatus,
  backorderOfId: order.backorderOfId,
  createdAt: order.createdAt,
});

/**
 * The prefill the server would answer (M22a) for an order whose every line
 * needs `required` per `quantity`: `required × (quantity + outputScrap) / order quantity`,
 * here on whole numbers only — enough for the fixtures.
 */
export const draftFor = (order: ManufacturingOrder, query: URLSearchParams): ManufacturingProductionDraft => {
  const quantity = query.get("quantity") ?? String(Number(order.quantity) - Number(order.produced));
  const outputScrap = query.get("outputScrap") ?? "0";
  const base = Number(quantity) + Number(outputScrap);
  return {
    quantity,
    outputScrap,
    toLocationId: order.outputLocationId,
    lines: order.components.map((line) => ({
      componentId: line.id ?? 0,
      variant: line.variant,
      required: line.required,
      remaining: line.remaining,
      reserved: line.reserved,
      proposed: ((Number(line.required) * base) / Number(order.quantity)).toFixed(3),
    })),
  };
};

export interface ManufacturingServer {
  meta?: Partial<ManufacturingMeta>;
  stats?: Partial<ManufacturingStats>;
  workCenters?: ManufacturingWorkCenter[];
  boms?: ManufacturingBom[];
  overview?: ManufacturingBomOverview;
  orders?: ManufacturingOrder[];
  events?: ManufacturingOrderEvent[];
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

export type ManufacturingStub = StubbedFetch;

export const parseBody = (body: BodyInit | null | undefined): unknown => {
  if (body === undefined || body === null) return undefined;
  try {
    return JSON.parse(String(body));
  } catch {
    return String(body);
  }
};

/** A refusal in the module's problem shape (§15). */
export const manufacturingProblem = (status: number, code: string | undefined, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ status, title: "Refused", code, ...extra }), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });

/**
 * A Manufacturing API answering what the pages ask: the meta, the stats, the
 * sites, the variant search, the work centers and the BoMs from the fixtures,
 * and the overview given. A write answers 200 with its body echoed onto the
 * record unless `write` answers it.
 */
export const stubManufacturingApi = (server: ManufacturingServer = {}): ManufacturingStub => {
  const meta: ManufacturingMeta = {
    canManageBoms: true,
    canSeeCosts: true,
    canManageOrders: false,
    canWorkShopFloor: false,
    currency: "NOK",
    ...server.meta,
  };
  const centers = server.workCenters ?? [workCenter(1, "SAW"), workCenter(2, "PAINT")];
  const boms = server.boms ?? [bomFixture()];
  const orders = server.orders ?? [orderFixture()];
  const stats: ManufacturingStats = {
    activeWorkCenters: centers.filter((one) => one.status === "active").length,
    blockedWorkCenters: centers.filter((one) => one.workingState === "blocked").length,
    activeBoms: boms.filter((one) => one.status === "active").length,
    ordersToStartToday: 0,
    ordersInProgress: 0,
    ordersLate: 0,
    ...server.stats,
  };

  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname.replace(/^\/api\/v1\/manufacturing/, "");
    const method = init?.method ?? "GET";
    if (method !== "GET") {
      const answer = server.write?.(method, path, parseBody(init?.body));
      if (answer) return Promise.resolve(answer);
      if (method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      const bom = /^\/boms(?:\/(\d+))?/.exec(path);
      if (bom) return Promise.resolve(jsonResponse(bom[1] ? 200 : 201, { ...bomFixture(), id: Number(bom[1] ?? 99) }));
      const order = /^\/orders(?:\/(\d+))?/.exec(path);
      if (order) {
        const found = orders.find((one) => one.id === Number(order[1])) ?? orderFixture({ id: 99, status: "draft" });
        return Promise.resolve(jsonResponse(order[1] ? 200 : 201, found));
      }
      const center = /^\/work-centers(?:\/(\d+))?/.exec(path);
      if (center)
        return Promise.resolve(jsonResponse(center[1] ? 200 : 201, workCenter(Number(center[1] ?? 99), "NEW")));
      return Promise.resolve(jsonResponse(404, {}));
    }
    if (path === "/meta") return Promise.resolve(jsonResponse(200, meta));
    if (path === "/stats") return Promise.resolve(jsonResponse(200, stats));
    if (path === "/sites") return Promise.resolve(jsonResponse(200, { items: sites() }));
    if (path === "/variants") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      return Promise.resolve(
        jsonResponse(200, {
          items: variants().filter((one) => `${one.sku} ${one.productName}`.toLowerCase().includes(q)),
        }),
      );
    }
    if (path === "/work-centers") {
      const status = url.searchParams.get("status") ?? "active";
      return Promise.resolve(
        jsonResponse(200, { items: centers.filter((one) => status === "all" || one.status === status) }),
      );
    }
    const center = /^\/work-centers\/(\d+)$/.exec(path);
    if (center) {
      const found = centers.find((one) => one.id === Number(center[1]));
      return Promise.resolve(found ? jsonResponse(200, found) : manufacturingProblem(404, undefined));
    }
    if (/^\/work-centers\/\d+\/closures$/.test(path)) return Promise.resolve(jsonResponse(200, { items: [] }));
    if (path === "/boms") {
      const status = url.searchParams.get("status") ?? "active";
      const items = boms.filter((one) => status === "all" || one.status === status).map(summaryOf);
      return Promise.resolve(jsonResponse(200, { items, total: items.length }));
    }
    const bom = /^\/boms\/(\d+)$/.exec(path);
    if (bom) {
      const found = boms.find((one) => one.id === Number(bom[1]));
      return Promise.resolve(found ? jsonResponse(200, found) : manufacturingProblem(404, undefined));
    }
    if (path === "/orders") {
      const items = orders.map(orderSummaryOf);
      return Promise.resolve(jsonResponse(200, { items, total: items.length, page: 1, pageSize: 50 }));
    }
    const order = /^\/orders\/(\d+)(\/events|\/production-draft)?$/.exec(path);
    if (order) {
      const found = orders.find((one) => one.id === Number(order[1]));
      if (!found) return Promise.resolve(manufacturingProblem(404, undefined));
      if (order[2] === "/events") return Promise.resolve(jsonResponse(200, { items: server.events ?? [] }));
      if (order[2] === "/production-draft")
        return Promise.resolve(jsonResponse(200, draftFor(found, url.searchParams)));
      return Promise.resolve(jsonResponse(200, found));
    }
    if (/^\/sites\/\d+\/locations$/.test(path)) return Promise.resolve(jsonResponse(200, { items: locations() }));
    if (path === "/people") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      return Promise.resolve(
        jsonResponse(200, { items: people().filter((one) => one.displayName.toLowerCase().includes(q)) }),
      );
    }
    if (/^\/boms\/\d+\/overview$/.test(path) && server.overview)
      return Promise.resolve(jsonResponse(200, server.overview));
    return Promise.resolve(jsonResponse(404, {}));
  }) as ManufacturingStub;
};
