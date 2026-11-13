import type { ManufacturingBom, ManufacturingBomOverview, ManufacturingBomSummary } from "../api/boms";
import type { ManufacturingLocation } from "../api/locations";
import type { ManufacturingMeta } from "../api/meta";
import type {
  ManufacturingOrder,
  ManufacturingOrderEvent,
  ManufacturingOrderLots,
  ManufacturingOrderSummary,
  ManufacturingProductionDraft,
  ManufacturingSalesOrderLine,
  ManufacturingUnbuild,
} from "../api/orders";
import type { ManufacturingPerson } from "../api/people";
import type { ManufacturingDeviation } from "../api/quality";
import type { ManufacturingReplenishmentRule, ManufacturingSuggestionList } from "../api/replenishment";
import type { ManufacturingSchedule, ManufacturingScheduleBar, ManufacturingScheduleDay } from "../api/schedule";
import type {
  ManufacturingMyWork,
  ManufacturingTimeLog,
  ManufacturingWorkCenterBlock,
  ManufacturingWorkOrder,
  ManufacturingWorkOrderView,
} from "../api/shop-floor";
import type { ManufacturingSite } from "../api/sites";
import type { ManufacturingStats } from "../api/stats";
import type { ManufacturingVariantSearchItem } from "../api/variants";
import type { ManufacturingWorkCenter, ManufacturingWorkCenterGroup } from "../api/work-centers";
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
    lotPattern: "{MO}",
    outputs: [],
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
        checks: [],
        workCenterGroupId: null,
        dependsOn: [],
        sendAheadQuantity: null,
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
    lotPattern: "{MO}",
    outputs: [],
    splitFromId: null,
    splitFrom: null,
    mergedIntoId: null,
    mergedInto: null,
    splitParts: [],
    mergedFrom: [],
    unbuilt: "0",
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
    requestedStart: null,
    requestedStartDefault: "2026-11-16",
    lateByDays: null,
    priority: "normal",
    responsible: null,
    note: "",
    origin: { kind: "manual", orderId: null, lineId: null, reference: null, cancelled: false },
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
    warnings: [],
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
    byProducts: [],
    output: {
      tracking: "none",
      lotPattern: order.lotPattern,
      lotNumber: null,
      productionDate: "2026-11-11",
      expiry: null,
      bestBefore: null,
      serialFirst: null,
    },
    lines: order.components.map((line) => ({
      componentId: line.id ?? 0,
      variant: line.variant,
      required: line.required,
      remaining: line.remaining,
      reserved: line.reserved,
      proposed: ((Number(line.required) * base) / Number(order.quantity)).toFixed(3),
      tracking: "none",
      lots: [],
      lotChoices: [],
      short: "0",
    })),
  };
};

/** The first of MO-12's work orders: cutting at SAW, ready, nobody on it. */
export const workOrderFixture = (patch: Partial<ManufacturingWorkOrder> = {}): ManufacturingWorkOrder => {
  const [table] = variants();
  return {
    id: 40,
    orderId: 5,
    order: {
      id: 5,
      number: "MO-12",
      variant: ref(table),
      quantity: "10",
      produced: "0",
      remaining: "10",
      dueDate: "2026-11-20",
      priority: "normal",
      status: "confirmed",
      reservationStatus: "reserved",
    },
    sequence: 10,
    name: "Cut",
    workCenter: { id: 1, code: "SAW", name: "SAW station", siteId: 1, workingState: "normal" },
    setupMinutes: "0",
    cleanupMinutes: "0",
    minutesPerCycle: "4",
    capacity: 1,
    timeEfficiency: 100,
    expectedMinutes: 40,
    loggedMinutes: 0,
    actualMinutes: 0,
    deviationMinutes: -40,
    deviationPercent: "-100.0",
    plannedStart: null,
    plannedFinish: null,
    status: "ready",
    startedAt: null,
    finishedAt: null,
    last: false,
    operators: [],
    revision: 1,
    ...patch,
  };
};

/** A closed productive log of Kari's on the first work order. */
export const timeLogFixture = (patch: Partial<ManufacturingTimeLog> = {}): ManufacturingTimeLog => ({
  id: 80,
  workOrderId: 40,
  workCenterId: 1,
  user: people()[0],
  kind: "productive",
  reason: null,
  note: "",
  startedAt: "2026-11-10T08:00:00Z",
  endedAt: "2026-11-10T09:30:00Z",
  quantity: null,
  forced: false,
  correctedBy: null,
  correctedAt: null,
  longRunning: false,
  ...patch,
});

/** A day of SAW's row: open 06:00–14:00 UTC, 480 minutes. */
export const scheduleDay = (date: string, patch: Partial<ManufacturingScheduleDay> = {}): ManufacturingScheduleDay => ({
  date,
  intervals: [{ start: `${date}T06:00:00Z`, end: `${date}T14:00:00Z` }],
  closed: null,
  capacityMinutes: 480,
  plannedMinutes: 0,
  loadPercent: "0.0",
  overloaded: false,
  ...patch,
});

/** MO-12's cutting at SAW on the board's first day, 08:00–09:00 UTC. */
export const scheduleBarFixture = (patch: Partial<ManufacturingScheduleBar> = {}): ManufacturingScheduleBar => ({
  workOrderId: 40,
  workOrderRevision: 3,
  sequence: 10,
  name: "Cut",
  status: "ready",
  orderId: 5,
  orderNumber: "MO-12",
  variant: ref(variants()[0]),
  urgent: false,
  late: false,
  startsAt: "2026-11-11T08:00:00Z",
  endsAt: "2026-11-11T09:00:00Z",
  plannedStart: "2026-11-11T08:00:00Z",
  plannedFinish: "2026-11-11T09:00:00Z",
  ...patch,
});

/**
 * Oslo's board for the week from 2026-11-11: SAW overloaded on the first
 * day, half loaded on the second, a holiday on the fourth; one bar; MO-13
 * waiting to be planned.
 */
export const scheduleFixture = (patch: Partial<ManufacturingSchedule> = {}): ManufacturingSchedule => {
  const dates = ["2026-11-11", "2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15", "2026-11-16", "2026-11-17"];
  return {
    siteId: 1,
    from: "2026-11-11",
    days: 7,
    today: "2026-11-11",
    workCenters: [
      {
        workCenter: { id: 1, code: "SAW", name: "SAW station", siteId: 1, workingState: "normal" },
        blocked: false,
        closures: [],
        days: dates.map((date, index) =>
          index === 0
            ? scheduleDay(date, { plannedMinutes: 540, loadPercent: "112.5", overloaded: true })
            : index === 1
              ? scheduleDay(date, { plannedMinutes: 240, loadPercent: "50.0" })
              : index === 3
                ? scheduleDay(date, { intervals: [], closed: "holiday", capacityMinutes: 0, loadPercent: null })
                : scheduleDay(date),
        ),
        bars: [scheduleBarFixture()],
      },
    ],
    unscheduled: [
      {
        id: 6,
        number: "MO-13",
        variant: ref(variants()[0]),
        quantity: "4",
        dueDate: "2026-11-30",
        requestedStart: null,
        priority: "urgent",
        status: "confirmed",
        revision: 2,
        expectedMinutes: 90,
      },
    ],
    ...patch,
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
  workOrders?: ManufacturingWorkOrder[];
  timeLogs?: ManufacturingTimeLog[];
  /** The work orders' views; by default each work order with no instructions, components or logs. */
  views?: Partial<ManufacturingWorkOrderView>[];
  /** A work center's block, by its id. */
  blocks?: Record<number, ManufacturingWorkCenterBlock>;
  myWork?: ManufacturingMyWork;
  schedule?: ManufacturingSchedule;
  /** The reports' JSON answers, by kind; an empty report of the period when absent. */
  reports?: Record<string, unknown>;
  /** The sales order lines read through Orders (M45), by line id; any other is 404. */
  salesOrderLines?: Record<number, ManufacturingSalesOrderLine>;
  /** Changes the production draft the fake answers (a tracked product or component's lots). */
  draft?: (draft: ManufacturingProductionDraft) => ManufacturingProductionDraft;
  /** The orders' lots (manufacturing phase 2 M83), by order id; none made or consumed when absent. */
  orderLots?: Record<number, ManufacturingOrderLots>;
  /** The deviations (manufacturing phase 2 M99), newest first. */
  deviations?: ManufacturingDeviation[];
  /** The replenishment suggestions (manufacturing phase 2 M111); none, never run, when absent. */
  suggestions?: ManufacturingSuggestionList;
  /** A variant's replenishment rules (M111); none when absent. */
  replenishmentRules?: ManufacturingReplenishmentRule[];
  /** Each queue's Group work (manufacturing phase 2 M119), by work center id; none when absent. */
  groupWork?: Record<number, ManufacturingWorkOrder[]>;
  /** The work center groups (M117); none when absent. */
  groups?: ManufacturingWorkCenterGroup[];
  /** The unbuilds (manufacturing phase 2 M123), newest first; none when absent. */
  unbuilds?: ManufacturingUnbuild[];
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
    canManageQuality: false,
    canTraceLots: false,
    currency: "NOK",
    ...server.meta,
  };
  const centers = server.workCenters ?? [workCenter(1, "SAW"), workCenter(2, "PAINT")];
  const boms = server.boms ?? [bomFixture()];
  const orders = server.orders ?? [orderFixture()];
  const workOrders = server.workOrders ?? [workOrderFixture()];
  const viewOf = (workOrder: ManufacturingWorkOrder): ManufacturingWorkOrderView => ({
    workOrder,
    instructions: "",
    components: [],
    openLogs: [],
    myOpenLog: null,
    block: server.blocks?.[workOrder.workCenter.id] ?? null,
    ...server.views?.find((one) => one.workOrder?.id === workOrder.id),
  });
  const stats: ManufacturingStats = {
    activeWorkCenters: centers.filter((one) => one.status === "active").length,
    blockedWorkCenters: centers.filter((one) => one.workingState === "blocked").length,
    activeBoms: boms.filter((one) => one.status === "active").length,
    ordersToStartToday: 0,
    ordersInProgress: 0,
    ordersLate: 0,
    suggestionsToMake: 0,
    today: "2026-11-11",
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
      const action = /^\/work-orders\/(\d+)\/(start|pause|finish|quantity)$/.exec(path);
      if (action) {
        const found = workOrders.find((one) => one.id === Number(action[1])) ?? workOrderFixture();
        return Promise.resolve(jsonResponse(200, { workOrder: viewOf(found), closedLog: null, production: null }));
      }
      const blocking = /^\/work-centers\/(\d+)\/(block|unblock)$/.exec(path);
      if (blocking) {
        const found = centers.find((one) => one.id === Number(blocking[1])) ?? centers[0];
        return Promise.resolve(
          jsonResponse(200, {
            workCenter: {
              id: found.id,
              code: found.code,
              name: found.name,
              siteId: found.siteId,
              workingState: "normal",
            },
            block: null,
            items: [],
          }),
        );
      }
      if (path === "/schedule/plan-all") return Promise.resolve(jsonResponse(200, { planned: [], noCapacity: [] }));
      if (/^\/work-orders\/\d+\/reschedule$/.test(path))
        return Promise.resolve(jsonResponse(200, { items: workOrders }));
      if (/^\/time-logs\/\d+$/.test(path) || /^\/work-orders\/\d+\/time-logs$/.test(path))
        return Promise.resolve(jsonResponse(200, timeLogFixture()));
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
    if (path === "/schedule") return Promise.resolve(jsonResponse(200, server.schedule ?? scheduleFixture()));
    const report = /^\/reports\/([a-z-]+)$/.exec(path);
    if (report) {
      const from = url.searchParams.get("from") ?? "2026-10-13";
      const to = url.searchParams.get("to") ?? "2026-11-11";
      if (url.searchParams.get("format") === "csv")
        return Promise.resolve(
          new Response("a;b\r\n", {
            status: 200,
            headers: {
              "Content-Type": "text/csv",
              "Content-Disposition": `attachment; filename="manufacturing-${report[1]}-${from}-${to}.csv"`,
            },
          }),
        );
      const empty = { from, to, siteId: null, ...(report[1] === "production-cost" ? { groups: [] } : { rows: [] }) };
      return Promise.resolve(jsonResponse(200, server.reports?.[report[1]] ?? empty));
    }
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
    const salesOrderLine = /^\/sales-order-lines\/(\d+)$/.exec(path);
    if (salesOrderLine) {
      const found = server.salesOrderLines?.[Number(salesOrderLine[1])];
      return Promise.resolve(found ? jsonResponse(200, found) : manufacturingProblem(404, undefined));
    }
    if (path === "/unbuilds") {
      const items = server.unbuilds ?? [];
      return Promise.resolve(jsonResponse(200, { items, total: items.length, page: 1, pageSize: 50 }));
    }
    if (path === "/orders") {
      const items = orders.map(orderSummaryOf);
      return Promise.resolve(jsonResponse(200, { items, total: items.length, page: 1, pageSize: 50 }));
    }
    const order = /^\/orders\/(\d+)(\/events|\/production-draft|\/lots)?$/.exec(path);
    if (order) {
      const found = orders.find((one) => one.id === Number(order[1]));
      if (!found) return Promise.resolve(manufacturingProblem(404, undefined));
      if (order[2] === "/events") return Promise.resolve(jsonResponse(200, { items: server.events ?? [] }));
      if (order[2] === "/lots")
        return Promise.resolve(
          jsonResponse(
            200,
            server.orderLots?.[found.id] ?? { outputs: [], components: [], byProducts: [], tracking: "none" },
          ),
        );
      if (order[2] === "/production-draft") {
        const draft = draftFor(found, url.searchParams);
        return Promise.resolve(jsonResponse(200, server.draft ? server.draft(draft) : draft));
      }
      return Promise.resolve(jsonResponse(200, found));
    }
    const orderWork = /^\/orders\/(\d+)\/(work-orders|time-logs)$/.exec(path);
    if (orderWork)
      return Promise.resolve(
        jsonResponse(200, {
          items:
            orderWork[2] === "work-orders"
              ? workOrders.filter((one) => one.orderId === Number(orderWork[1]))
              : (server.timeLogs ?? []),
        }),
      );
    const queue = /^\/work-centers\/(\d+)\/queue$/.exec(path);
    if (queue) {
      const found = centers.find((one) => one.id === Number(queue[1]));
      if (!found) return Promise.resolve(manufacturingProblem(404, undefined));
      const block = server.blocks?.[found.id] ?? null;
      return Promise.resolve(
        jsonResponse(200, {
          workCenter: {
            id: found.id,
            code: found.code,
            name: found.name,
            siteId: found.siteId,
            workingState: block ? "blocked" : "normal",
          },
          block,
          items: workOrders.filter(
            (one) => one.workCenter.id === found.id && !["done", "cancelled", "pending"].includes(one.status),
          ),
          groupWork: server.groupWork?.[found.id] ?? [],
        }),
      );
    }
    if (path === "/work-center-groups") return Promise.resolve(jsonResponse(200, { items: server.groups ?? [] }));
    const workOrder = /^\/work-orders\/(\d+)$/.exec(path);
    if (workOrder) {
      const found = workOrders.find((one) => one.id === Number(workOrder[1]));
      return Promise.resolve(found ? jsonResponse(200, viewOf(found)) : manufacturingProblem(404, undefined));
    }
    if (path === "/shop-floor/mine")
      return Promise.resolve(jsonResponse(200, server.myWork ?? { openLog: null, openWorkOrder: null, today: [] }));
    if (/^\/sites\/\d+\/locations$/.test(path)) return Promise.resolve(jsonResponse(200, { items: locations() }));
    if (path === "/people") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      return Promise.resolve(
        jsonResponse(200, { items: people().filter((one) => one.displayName.toLowerCase().includes(q)) }),
      );
    }
    if (path === "/deviations") {
      const items = (server.deviations ?? []).filter(
        (one) => !url.searchParams.get("status") || one.status === url.searchParams.get("status"),
      );
      return Promise.resolve(jsonResponse(200, { items, total: items.length }));
    }
    const deviation = /^\/deviations\/(\d+)$/.exec(path);
    if (deviation) {
      const found = server.deviations?.find((one) => one.id === Number(deviation[1]));
      return Promise.resolve(found ? jsonResponse(200, found) : manufacturingProblem(404, undefined));
    }
    if (path === "/replenishment/suggestions")
      return Promise.resolve(jsonResponse(200, server.suggestions ?? { sites: [], lastRun: null, salesCounted: true }));
    if (path === "/replenishment/rules")
      return Promise.resolve(jsonResponse(200, { items: server.replenishmentRules ?? [] }));
    if (/^\/boms\/\d+\/overview$/.test(path) && server.overview)
      return Promise.resolve(jsonResponse(200, server.overview));
    return Promise.resolve(jsonResponse(404, {}));
  }) as ManufacturingStub;
};
