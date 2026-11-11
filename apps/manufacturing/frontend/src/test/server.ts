import type { ManufacturingBom, ManufacturingBomOverview, ManufacturingBomSummary } from "../api/boms";
import type { ManufacturingMeta } from "../api/meta";
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

export interface ManufacturingServer {
  meta?: Partial<ManufacturingMeta>;
  stats?: Partial<ManufacturingStats>;
  workCenters?: ManufacturingWorkCenter[];
  boms?: ManufacturingBom[];
  overview?: ManufacturingBomOverview;
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
  const stats: ManufacturingStats = {
    activeWorkCenters: centers.filter((one) => one.status === "active").length,
    blockedWorkCenters: centers.filter((one) => one.workingState === "blocked").length,
    activeBoms: boms.filter((one) => one.status === "active").length,
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
    if (/^\/boms\/\d+\/overview$/.test(path) && server.overview)
      return Promise.resolve(jsonResponse(200, server.overview));
    return Promise.resolve(jsonResponse(404, {}));
  }) as ManufacturingStub;
};
