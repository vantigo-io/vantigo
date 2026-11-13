import type { InventoryCount, InventoryCountLine } from "../api/counts";
import type { InventoryImportRow } from "../api/imports";
import type { InventoryLocation, InventoryLocationRef } from "../api/locations";
import type { InventoryLot, InventoryLotHoldInput } from "../api/lots";
import type { InventoryMeta } from "../api/meta";
import type { InventoryMovement, InventoryMovementKind } from "../api/movements";
import type { InventoryLowStockRow, InventoryPlacement, InventoryPlacementImportRow } from "../api/placements";
import type { InventorySite, InventorySiteRef } from "../api/sites";
import type { InventoryProductStock, InventoryVariantRef, InventoryVariantStock } from "../api/stock";
import type { InventoryTrace } from "../api/trace";
import type { InventoryTransfer, InventoryTransferLine } from "../api/transfers";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

const STAMP = "2026-11-02T09:00:00Z";

/** Two sites: Oslo's warehouse with an aisle and two racks, Bergen's store with a sales floor. */
export const sites = (): InventorySite[] => [
  {
    id: 1,
    code: "OSL",
    name: "Oslo lager",
    kind: "warehouse",
    status: "active",
    rootLocationId: 10,
    locationCount: 0,
    revision: 1,
    fulfilsOrders: true,
    address: { city: "Oslo", country: "NO" },
    createdAt: STAMP,
    updatedAt: STAMP,
  },
  {
    id: 2,
    code: "BGO",
    name: "Bergen butikk",
    kind: "store",
    status: "active",
    rootLocationId: 20,
    locationCount: 0,
    revision: 1,
    fulfilsOrders: false,
    createdAt: STAMP,
    updatedAt: STAMP,
  },
];

const location = (
  id: number,
  siteId: number,
  fullCode: string,
  kind: InventoryLocation["kind"],
  parentId?: number,
  name?: string,
): InventoryLocation => ({
  id,
  siteId,
  fullCode,
  code: fullCode.split("-").pop() ?? fullCode,
  kind,
  depth: fullCode.split("-").length - 1,
  sortOrder: 0,
  status: "active",
  revision: 1,
  createdAt: STAMP,
  updatedAt: STAMP,
  ...(parentId === undefined ? {} : { parentId }),
  ...(name ? { name } : {}),
});

export const locations = (): InventoryLocation[] => [
  location(10, 1, "OSL", "site"),
  location(11, 1, "OSL-A", "aisle", 10, "Aisle A"),
  location(12, 1, "OSL-A-01", "rack", 11),
  location(13, 1, "OSL-A-02", "rack", 11),
  location(20, 2, "BGO", "site"),
  location(21, 2, "BGO-SALG", "sales_floor", 20, "Butikkgulv"),
];

/** The catalog: a T-shirt in two sizes (product 1) and a bolt (product 2). */
export const variants = (): InventoryVariantRef[] => [
  { id: 101, productId: 1, sku: "TS-M", productName: "T-shirt", unit: "pcs", barcode: "7070000000101" },
  { id: 102, productId: 1, sku: "TS-L", productName: "T-shirt", unit: "pcs" },
  { id: 201, productId: 2, sku: "BOLT-10", productName: "Bolt", unit: "pcs" },
];

export interface Balance {
  variantId: number;
  locationId: number;
  onHand: number;
}

/** A variant's placement at a site (I54), as the fixtures write it. */
export interface Placement {
  variantId: number;
  siteId: number;
  primaryLocationId: number | null;
  reorderPoint: number | null;
  maximum: number | null;
  /** Manufacturing phase 2 M105; absent is unset. */
  multiple?: number | null;
  revision: number;
}

/** What orders have reserved of a variant at a site (orders design O45). */
export interface Reservation {
  variantId: number;
  siteId: number;
  quantity: number;
}

export interface InventoryServer {
  /** Fields of `/meta` over a caller who may do everything. */
  meta?: Partial<InventoryMeta>;
  sites?: InventorySite[];
  locations?: InventoryLocation[];
  variants?: InventoryVariantRef[];
  balances?: Balance[];
  reservations?: Reservation[];
  movements?: InventoryMovement[];
  /** Answers `GET /products/{id}/stock` instead of working it out: a Service product's empty answer. */
  productStock?: Record<number, InventoryProductStock>;
  counts?: InventoryCount[];
  transfers?: InventoryTransfer[];
  placements?: Placement[];
  /** Answers `GET /low-stock` from these rows, filtered by site and search, `lowStockPageSize` at a time. */
  lowStock?: InventoryLowStockRow[];
  lowStockPageSize?: number;
  /** The lots `GET /lots` and `GET /lots/{id}` answer, whose holds the page's writes change (manufacturing phase 2 M60–M64). */
  lots?: InventoryLot[];
  /** How many locations a site's list answers before it says it was truncated; 10,000 as the server's. */
  locationLimit?: number;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
  /** A variant's rules, by variant id (manufacturing phase 2 M100); none — off at revision 0 — when absent. */
  rules?: Record<number, { inspectOnReceipt: boolean; revision: number }>;
  /** The lots awaiting inspection the stats answer (M100). */
  lotsAwaitingInspection?: number;
  /** Answers `GET /trace` for its query (manufacturing phase 2 M89); the exports answer bytes that do not matter. */
  trace?: (query: URLSearchParams) => InventoryTrace | Response;
}

export interface InventoryState {
  sites: InventorySite[];
  locations: InventoryLocation[];
  balances: Balance[];
  reservations: Reservation[];
  movements: InventoryMovement[];
  counts: InventoryCount[];
  transfers: InventoryTransfer[];
  placements: Placement[];
  lots: InventoryLot[];
}

/** A transfer line as the fixtures write it: a variant, what was asked, and what has happened to it so far. */
export const transferLine = (
  id: number,
  variant: InventoryVariantRef,
  quantity: number,
  totals: Partial<Pick<InventoryTransferLine, "sent" | "received" | "missing" | "returned" | "fromLocation">> = {},
): InventoryTransferLine => {
  const sent = totals.sent ?? 0;
  const received = totals.received ?? 0;
  const missing = totals.missing ?? 0;
  const returned = totals.returned ?? 0;
  return {
    id,
    lineNo: id,
    variant,
    quantity,
    sent,
    received,
    missing,
    returned,
    inTransit: sent - received - missing - returned,
    ...(totals.fromLocation ? { fromLocation: totals.fromLocation } : {}),
  };
};

/** A transfer from Oslo to Bergen, a draft unless the fixture says otherwise. */
export const transferFixture = (
  fields: Partial<InventoryTransfer> & Pick<InventoryTransfer, "lines">,
): InventoryTransfer => {
  const [osl, bgo] = sites();
  const ref = (site: InventorySite) => ({
    id: site.id,
    code: site.code,
    name: site.name,
    kind: site.kind,
    status: site.status,
  });
  return {
    id: 5,
    number: 1042,
    status: "draft",
    fromSite: ref(osl),
    toSite: ref(bgo),
    revision: 1,
    createdAt: STAMP,
    createdBy: { displayName: "Kari N." },
    updatedAt: STAMP,
    actions: [],
    ...fields,
    inTransit: fields.lines.reduce((sum, line) => sum + line.inTransit, 0),
  };
};

/** A Euro pallet of 600 and a box of 15, with their barcodes, for a variant that is stocked in packagings (I24). */
export const packagings = () => [
  { id: 71, name: "Euro pallet", kind: "pallet", quantity: 600, barcode: "17070000000201" },
  { id: 72, name: "Box", kind: "case", quantity: 15, barcode: "27070000000201" },
];

/** What an entered quantity makes in the base unit, as the server works it out at posting (I25). */
type Entered = { packagings: { packagingId: number; count: number }[]; loose?: number };

export type InventoryStub = StubbedFetch & { state: InventoryState };

/** A request's body: JSON parsed, anything else — a CSV upload — as its text. */
export const parseBody = (body: BodyInit | null | undefined): unknown => {
  if (body === undefined || body === null) return undefined;
  // A file sent as its bytes (the import) reads back as its text; the fake's files are UTF-8.
  if (Object.prototype.toString.call(body) === "[object ArrayBuffer]")
    return new TextDecoder().decode(body as ArrayBuffer);
  try {
    return JSON.parse(String(body));
  } catch {
    return String(body);
  }
};

/** A refusal in the module's problem shape (design §4). */
export const inventoryProblem = (status: number, code: string | undefined, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ status, title: extra.title ?? "Refused", code, ...extra }), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });

/**
 * An in-memory Inventory API answering what the pages ask as the server
 * would: the meta, sites and their locations, a location's detail and
 * contents, the lookup (LOC token, full code, SKU, barcode), the variant
 * search, product and variant stock worked out from the balances, the
 * movements by filter, and the three postings, which move the balances.
 */
export const stubInventoryApi = (server: InventoryServer = {}): InventoryStub => {
  const state: InventoryState = {
    sites: server.sites ?? sites(),
    locations: server.locations ?? locations(),
    balances: server.balances ?? [],
    reservations: server.reservations ?? [],
    movements: server.movements ?? [],
    counts: server.counts ?? [],
    transfers: server.transfers ?? [],
    placements: server.placements ?? [],
    lots: server.lots ?? [],
  };
  const catalog = server.variants ?? variants();
  let nextId = 900;
  /** The placements files committed so far: the same file again writes nothing and answers 200 (I61). */
  const importedPlacements = new Set<string>();

  const meta: InventoryMeta = {
    canViewLocations: true,
    canManageLocations: true,
    canViewStock: true,
    canAdjust: true,
    canReceive: true,
    canCount: true,
    canTransfer: true,
    canOverrideExpired: false,
    canManageLots: true,
    today: "2026-09-12",
    ...server.meta,
  };
  const siteRef = (site: InventorySite): InventorySiteRef => ({
    id: site.id,
    code: site.code,
    name: site.name,
    kind: site.kind,
    status: site.status,
  });
  const locationOf = (id: number) => state.locations.find((one) => one.id === id);
  const ref = (one: InventoryLocation): InventoryLocationRef => ({
    id: one.id,
    siteId: one.siteId,
    fullCode: one.fullCode,
    kind: one.kind,
    status: one.status,
    ...(one.name ? { name: one.name } : {}),
  });
  const variantOf = (id: number) => catalog.find((one) => one.id === id);
  const onHandOf = (variantId: number, locationId: number) =>
    state.balances.find((one) => one.variantId === variantId && one.locationId === locationId)?.onHand ?? 0;
  /** A line's base quantity: its quantity, or its entered packagings at their content now. */
  const amount = (line: { variantId: number; quantity?: number; entered?: Entered; decrease?: boolean }) => {
    if (!line.entered) return line.quantity ?? 0;
    const known = variantOf(line.variantId)?.packagings ?? [];
    const total = line.entered.packagings.reduce(
      (sum, one) => sum + one.count * (known.find((p) => p.id === one.packagingId)?.quantity ?? 0),
      line.entered.loose ?? 0,
    );
    return line.decrease ? -total : total;
  };
  const snapshot = (variantId: number, entered?: Entered) =>
    entered && {
      packagings: entered.packagings.map((one) => {
        const known = variantOf(variantId)?.packagings?.find((p) => p.id === one.packagingId);
        return { ...one, name: known?.name ?? "?", quantity: known?.quantity ?? 0 };
      }),
      ...(entered.loose ? { loose: entered.loose } : {}),
    };
  const siteFigures = (site: InventorySite): InventorySite => {
    const inSite = new Set(state.locations.filter((one) => one.siteId === site.id).map((one) => one.id));
    const lastReceipt = state.movements.find((one) => one.kind === "receipt" && one.site.id === site.id);
    return {
      ...site,
      locationCount: state.locations.filter(
        (one) => one.siteId === site.id && one.status === "active" && one.kind !== "site",
      ).length,
      ...(meta.canViewStock
        ? {
            stockLines: state.balances.filter((one) => one.onHand !== 0 && inSite.has(one.locationId)).length,
            incomingTransfers: state.transfers.filter(
              (one) => one.toSite.id === site.id && (one.status === "sent" || one.status === "partly_received"),
            ).length,
            outgoingTransfers: state.transfers.filter(
              (one) => one.fromSite.id === site.id && (one.status === "sent" || one.status === "partly_received"),
            ).length,
            ...(lastReceipt ? { lastReceiptAt: lastReceipt.postedAt } : {}),
          }
        : {}),
    };
  };
  /** A count as the server answers it: each line's balance now, moved when it differs from what was expected. */
  const countAnswer = (count: InventoryCount, withLines = true): InventoryCount => {
    const lines = (count.lines ?? [])
      .map((line) => {
        const current = count.status === "counting" ? onHandOf(line.variant.id, line.location.id) : line.current;
        return { ...line, current, moved: count.status === "counting" ? current !== line.expected : line.moved };
      })
      .sort(
        (a, b) => a.location.fullCode.localeCompare(b.location.fullCode) || a.variant.sku.localeCompare(b.variant.sku),
      );
    const rest = { ...count };
    delete rest.lines;
    return {
      ...rest,
      lineCount: lines.length,
      countedLines: lines.filter((line) => line.counted !== undefined).length,
      movedLines: lines.filter((line) => line.moved).length,
      ...(withLines ? { lines } : {}),
    };
  };
  const below = (id: number) => {
    const ids = new Set([id]);
    for (const one of [...state.locations].sort((a, b) => a.depth - b.depth))
      if (one.parentId !== undefined && ids.has(one.parentId)) ids.add(one.id);
    return ids;
  };
  const rowsOf = (balances: Balance[]) =>
    balances
      .filter((one) => one.onHand !== 0)
      .map((one) => {
        const where = locationOf(one.locationId);
        const what = variantOf(one.variantId);
        if (!where || !what) throw new Error(`the fixture balance ${JSON.stringify(one)} names nothing`);
        return { variant: what, location: ref(where), onHand: one.onHand };
      })
      .sort(
        (a, b) => a.location.fullCode.localeCompare(b.location.fullCode) || a.variant.sku.localeCompare(b.variant.sku),
      );
  /** What is in transit of a variant to a site on its open transfers (I46). */
  const incomingAt = (variantId: number, siteId: number) =>
    state.transfers
      .filter((one) => one.toSite.id === siteId)
      .flatMap((one) => one.lines)
      .filter((line) => line.variant.id === variantId)
      .reduce((sum, line) => sum + line.inTransit, 0);
  const placementAt = (variantId: number, siteId: number) =>
    state.placements.find((one) => one.variantId === variantId && one.siteId === siteId);
  /** A placement as the reads answer it, low when available plus incoming is at or below its reorder point (I56). */
  const placementAnswer = (one: Placement | undefined, available: number, incoming: number) =>
    one && {
      primaryLocationId: one.primaryLocationId,
      reorderPoint: one.reorderPoint,
      maximum: one.maximum,
      multiple: one.multiple ?? null,
      revision: one.revision,
      low: one.reorderPoint !== null && available + incoming <= one.reorderPoint,
    };
  /** A tracked variant's lots on a location, as the fixture lots' `locations` place them, in the order given. */
  const lotsOn = (variantId: number, locationId: number) => {
    const lots = state.lots.flatMap((one) => {
      const at = one.variant.id === variantId ? one.locations.find((row) => row.location.id === locationId) : undefined;
      return at
        ? [
            {
              lotId: one.id,
              number: one.number,
              status: one.status,
              expired: one.expired,
              expiry: one.expiry,
              bestBefore: one.bestBefore,
              receivedAt: one.receivedAt,
              onHand: at.onHand,
            },
          ]
        : [];
    });
    return lots.length > 0 ? { lots } : {};
  };
  const variantStock = (variant: InventoryVariantRef): InventoryVariantStock => {
    const rows = rowsOf(state.balances.filter((one) => one.variantId === variant.id));
    const reservedAt = (siteId?: number) =>
      state.reservations
        .filter((one) => one.variantId === variant.id && (siteId === undefined || one.siteId === siteId))
        .reduce((sum, one) => sum + one.quantity, 0);
    const perSite = state.sites
      .map((site) => {
        const here = rows.filter((row) => row.location.siteId === site.id);
        const onHand = here.reduce((sum, row) => sum + row.onHand, 0);
        const reserved = reservedAt(site.id);
        const incoming = incomingAt(variant.id, site.id);
        const placement = placementAt(variant.id, site.id);
        // The primary location first, marked (I55).
        const ordered = here
          .map((row) => ({
            ...row,
            primary: row.location.id === placement?.primaryLocationId,
            ...lotsOn(variant.id, row.location.id),
          }))
          .sort((a, b) => Number(b.primary) - Number(a.primary));
        return {
          site: siteRef(site),
          onHand,
          reserved,
          available: onHand - reserved,
          held: 0,
          incoming,
          rows: ordered,
          ...(placement ? { placement: placementAnswer(placement, onHand - reserved, incoming) } : {}),
        };
      })
      .filter((one) => one.rows.length > 0 || one.reserved !== 0 || one.incoming !== 0);
    const totalOnHand = rows.reduce((sum, row) => sum + row.onHand, 0);
    const totalReserved = reservedAt();
    return { variant, totalOnHand, totalReserved, totalAvailable: totalOnHand - totalReserved, sites: perSite };
  };
  const move = (variantId: number, locationId: number, change: number) => {
    const balance = state.balances.find((one) => one.variantId === variantId && one.locationId === locationId);
    if (balance) balance.onHand += change;
    else state.balances.push({ variantId, locationId, onHand: change });
  };
  const posted = (
    kind: InventoryMovementKind,
    body: Record<string, unknown>,
    lines: { variantId: number; from?: number; to?: number; quantity: number; entered?: Entered }[],
    extra: Partial<InventoryMovement> = {},
  ) => {
    const site = state.sites.find((one) => one.id === body.siteId) ?? state.sites[0];
    const movement: InventoryMovement = {
      id: ++nextId,
      kind,
      clientRequestId: String(body.clientRequestId),
      site: siteRef(site),
      postedAt: STAMP,
      postedBy: { displayName: "Test User" },
      ...(typeof body.reason === "string" ? { reason: body.reason as InventoryMovement["reason"] } : {}),
      ...(typeof body.note === "string" ? { note: body.note } : {}),
      ...(typeof body.supplierName === "string" ? { supplierName: body.supplierName } : {}),
      ...extra,
      lines: lines.map((line, index) => {
        const from = line.from === undefined ? undefined : locationOf(line.from);
        const to = line.to === undefined ? undefined : locationOf(line.to);
        if (line.from !== undefined) move(line.variantId, line.from, -line.quantity);
        if (line.to !== undefined) move(line.variantId, line.to, line.quantity);
        return {
          lineNo: index + 1,
          variant: variantOf(line.variantId) ?? catalog[0],
          quantity: line.quantity,
          ...(line.entered ? { entered: snapshot(line.variantId, line.entered) } : {}),
          ...(from ? { from: ref(from) } : {}),
          ...(to ? { to: ref(to) } : {}),
        };
      }),
    };
    state.movements.unshift(movement);
    return jsonResponse(201, movement);
  };

  const transferAnswer = (one: InventoryTransfer): InventoryTransfer => ({
    ...one,
    lines: one.lines.map((line) => ({
      ...line,
      inTransit: line.sent - line.received - line.missing - line.returned,
    })),
    inTransit: one.lines.reduce((sum, line) => sum + line.sent - line.received - line.missing - line.returned, 0),
  });
  const stale = (one: InventoryTransfer, headers: Headers) =>
    headers.get("If-Match") !== String(one.revision)
      ? inventoryProblem(409, "revision_conflict", { title: "Revision conflict" })
      : undefined;
  const transferRoutes = (
    method: string,
    path: string,
    query: URLSearchParams,
    body: unknown,
    headers: Headers,
  ): Response | undefined => {
    if (path === "/stock/sites") {
      const variantIds = (query.get("variantIds") ?? "").split(",").filter(Boolean).map(Number);
      const siteIds = (query.get("siteIds") ?? "").split(",").filter(Boolean).map(Number);
      return jsonResponse(200, {
        stock: variantIds.flatMap((variantId) =>
          siteIds.map((siteId) => {
            const held = state.balances
              .filter(
                (one) =>
                  one.variantId === variantId && one.onHand !== 0 && locationOf(one.locationId)?.siteId === siteId,
              )
              .sort((a, b) => b.onHand - a.onHand);
            const onHand = held.reduce((sum, one) => sum + one.onHand, 0);
            const reserved = state.reservations
              .filter((one) => one.variantId === variantId && one.siteId === siteId)
              .reduce((sum, one) => sum + one.quantity, 0);
            const placement = placementAt(variantId, siteId);
            const incoming = incomingAt(variantId, siteId);
            return {
              variantId,
              siteId,
              onHand,
              reserved,
              available: onHand - reserved,
              incoming,
              locations: held.map((one) => ({
                location: ref(locationOf(one.locationId) as InventoryLocation),
                onHand: one.onHand,
                primary: one.locationId === placement?.primaryLocationId,
              })),
              ...(placement ? { placement: placementAnswer(placement, onHand - reserved, incoming) } : {}),
            };
          }),
        ),
      });
    }
    if (path === "/transfers" && method === "GET") {
      const status = query.get("status");
      const q = (query.get("q") ?? "").toUpperCase();
      const variantId = Number(query.get("variantId") ?? 0);
      const rows = state.transfers.filter(
        (one) =>
          (!status ||
            (status === "open" ? ["draft", "sent", "partly_received"].includes(one.status) : one.status === status)) &&
          (!query.get("fromSiteId") || one.fromSite.id === Number(query.get("fromSiteId"))) &&
          (!query.get("toSiteId") || one.toSite.id === Number(query.get("toSiteId"))) &&
          (!variantId || one.lines.some((line) => line.variant.id === variantId)) &&
          (query.get("withMissing") !== "true" ||
            one.lines.some((line) => line.missing > 0 && (!variantId || line.variant.id === variantId))) &&
          (!q || String(one.number) === q || one.lines.some((line) => line.variant.sku.toUpperCase() === q)),
      );
      return jsonResponse(200, {
        transfers: rows.map((one) => ({
          id: one.id,
          number: one.number,
          status: one.status,
          fromSite: one.fromSite,
          toSite: one.toSite,
          createdAt: one.createdAt,
          updatedAt: one.updatedAt,
          lineCount: one.lines.length,
          inTransit: transferAnswer(one).inTransit,
          missing: one.lines.reduce((sum, line) => sum + line.missing, 0),
          overdue: one.reference === "overdue",
          ...(one.reference ? { reference: one.reference } : {}),
          ...(one.expectedArrival ? { expectedArrival: one.expectedArrival } : {}),
          ...(one.sentAt ? { sentAt: one.sentAt } : {}),
        })),
      });
    }
    const draftLines = (input: {
      lines: { variantId: number; quantity?: number; entered?: Entered; suggestion?: unknown }[];
    }) =>
      input.lines.map((line, index) => ({
        ...transferLine(++nextId, variantOf(line.variantId) ?? catalog[0], amount(line)),
        lineNo: index + 1,
        ...(line.entered ? { entered: snapshot(line.variantId, line.entered) } : {}),
        ...(line.suggestion ? { suggestion: line.suggestion as InventoryTransferLine["suggestion"] } : {}),
      }));
    const siteRefOf = (siteId: number) => siteRef(state.sites.find((one) => one.id === siteId) ?? state.sites[0]);
    if (path === "/transfers" && method === "POST") {
      const input = body as Parameters<typeof draftLines>[0] & {
        fromSiteId: number;
        toSiteId: number;
        reference?: string;
        note?: string;
        expectedArrival?: string;
      };
      if (input.fromSiteId === input.toSiteId) return inventoryProblem(400, "transfer_same_site");
      const created: InventoryTransfer = transferAnswer({
        id: ++nextId,
        number: 1001 + state.transfers.length,
        status: "draft",
        fromSite: siteRefOf(input.fromSiteId),
        toSite: siteRefOf(input.toSiteId),
        revision: 1,
        createdAt: STAMP,
        createdBy: { displayName: "Test User" },
        updatedAt: STAMP,
        actions: [],
        inTransit: 0,
        lines: draftLines(input),
        ...(input.reference ? { reference: input.reference } : {}),
        ...(input.note ? { note: input.note } : {}),
        ...(input.expectedArrival ? { expectedArrival: input.expectedArrival } : {}),
      });
      state.transfers.unshift(created);
      return jsonResponse(201, created);
    }
    const match = /^\/transfers\/(\d+)(\/send|\/receive|\/cancel|\/note\.pdf)?$/.exec(path);
    if (!match) return undefined;
    const at = state.transfers.findIndex((one) => one.id === Number(match[1]));
    const one = state.transfers[at];
    if (!one) return new Response(null, { status: 404 });
    const action = match[2];
    const save = (next: InventoryTransfer) => {
      const answered = transferAnswer(next);
      state.transfers[at] = answered;
      return jsonResponse(200, answered);
    };
    if (!action && method === "GET") return jsonResponse(200, transferAnswer(one));
    if (action === "/note.pdf")
      return new Response(new Blob(["%PDF-1.7 note"], { type: "application/pdf" }), {
        status: 200,
        headers: { "Content-Type": "application/pdf" },
      });
    if (!action && method === "PUT") {
      const conflict = stale(one, headers);
      if (conflict) return conflict;
      if (one.status !== "draft") return inventoryProblem(409, "transfer_not_draft", { title: "Not a draft" });
      const input = body as Parameters<typeof draftLines>[0] & {
        fromSiteId: number;
        toSiteId: number;
        reference?: string;
      };
      return save({
        ...one,
        fromSite: siteRefOf(input.fromSiteId),
        toSite: siteRefOf(input.toSiteId),
        reference: input.reference,
        revision: one.revision + 1,
        lines: draftLines(input),
      });
    }
    if (!action && method === "DELETE") {
      const conflict = stale(one, headers);
      if (conflict) return conflict;
      state.transfers.splice(at, 1);
      return new Response(null, { status: 204 });
    }
    const movement = (
      kind: InventoryMovementKind,
      siteId: number,
      lines: { variantId: number; from?: number; to?: number; quantity: number }[],
    ) => {
      posted(kind, { siteId, clientRequestId: `transfer-${one.id}-${kind}` }, lines, {
        transfer: { id: one.id, number: one.number },
      });
      return state.movements[0].id;
    };
    if (action === "/send") {
      const conflict = stale(one, headers);
      if (conflict) return conflict;
      const input = body as {
        lines: { lineId: number; fromLocationId?: number }[];
        receive?: { lines: { lineId: number; toLocationId: number }[] };
      };
      const refused = input.lines
        .map((line, index) => ({ line, index }))
        .filter(({ line }) => line.fromLocationId === undefined)
        .map(({ index }) => ({ index, code: "from_location_required", detail: "No source." }));
      if (refused.length > 0) return inventoryProblem(400, undefined, { lines: refused });
      const sourceOf = (lineId: number) => input.lines.find((line) => line.lineId === lineId)?.fromLocationId ?? 0;
      const sentId = movement(
        "transfer_out",
        one.fromSite.id,
        one.lines.map((line) => ({ variantId: line.variant.id, from: sourceOf(line.id), quantity: line.quantity })),
      );
      const lines = one.lines.map((line) => {
        const from = locationOf(sourceOf(line.id));
        return { ...line, sent: line.quantity, ...(from ? { fromLocation: ref(from) } : {}) };
      });
      const actions = [
        ...one.actions,
        {
          id: ++nextId,
          kind: "send" as const,
          movementId: sentId,
          postedAt: STAMP,
          postedBy: { displayName: "Test User" },
          lines: lines.map((line) => ({
            kind: "sent" as const,
            lineId: line.id,
            quantity: line.sent,
            location: line.fromLocation,
          })),
        },
      ];
      if (!input.receive)
        return save({
          ...one,
          status: "sent",
          sentAt: STAMP,
          sentBy: { displayName: "Test User" },
          revision: one.revision + 1,
          lines,
          actions,
        });
      const destinationOf = (lineId: number) =>
        input.receive?.lines.find((line) => line.lineId === lineId)?.toLocationId ?? 0;
      const receivedId = movement(
        "transfer_in",
        one.toSite.id,
        lines.map((line) => ({ variantId: line.variant.id, to: destinationOf(line.id), quantity: line.sent })),
      );
      return save({
        ...one,
        status: "received",
        sentAt: STAMP,
        revision: one.revision + 1,
        lines: lines.map((line) => ({ ...line, received: line.sent })),
        actions: [
          ...actions,
          {
            id: ++nextId,
            kind: "receive" as const,
            movementId: receivedId,
            postedAt: STAMP,
            postedBy: { displayName: "Test User" },
            lines: lines.map((line) => ({ kind: "received" as const, lineId: line.id, quantity: line.sent })),
          },
        ],
      });
    }
    if (action === "/receive") {
      if (one.status !== "sent" && one.status !== "partly_received")
        return inventoryProblem(409, "transfer_not_receivable", { title: "Not receivable" });
      const input = body as {
        lines: { lineId: number; toLocationId: number; quantity?: number; entered?: Entered }[];
        close?: { reason: InventoryTransfer["closeReason"]; note?: string };
      };
      const current = transferAnswer(one);
      const refused = input.lines
        .map((line, index) => ({ line, index }))
        .filter(({ line }) => {
          const held = current.lines.find((one) => one.id === line.lineId);
          return !held || amount({ ...line, variantId: held.variant.id }) > held.inTransit;
        })
        .map(({ index }) => ({ index, code: "transfer_over_received", detail: "Too many." }));
      if (refused.length > 0) return inventoryProblem(400, undefined, { lines: refused });
      const now = input.lines.map((line) => {
        const held = current.lines.find((one) => one.id === line.lineId) as InventoryTransferLine;
        return { line, held, quantity: amount({ ...line, variantId: held.variant.id }) };
      });
      const movementId =
        now.length > 0
          ? movement(
              "transfer_in",
              one.toSite.id,
              now.map(({ line, held, quantity }) => ({ variantId: held.variant.id, to: line.toLocationId, quantity })),
            )
          : undefined;
      let lines = current.lines.map((line) => {
        const got = now.filter((one) => one.held.id === line.id).reduce((sum, one) => sum + one.quantity, 0);
        return { ...line, received: line.received + got, inTransit: line.inTransit - got };
      });
      if (input.close) lines = lines.map((line) => ({ ...line, missing: line.missing + line.inTransit, inTransit: 0 }));
      const done = lines.every((line) => line.inTransit === 0);
      return save({
        ...one,
        status: done ? "received" : "partly_received",
        ...(input.close ? { closeReason: input.close.reason, closeNote: input.close.note } : {}),
        lines,
        actions: [
          ...one.actions,
          {
            id: ++nextId,
            kind: "receive" as const,
            ...(movementId ? { movementId } : {}),
            postedAt: STAMP,
            postedBy: { displayName: "Test User" },
            lines: now.map(({ held, quantity }) => ({ kind: "received" as const, lineId: held.id, quantity })),
          },
        ],
      });
    }
    if (action === "/cancel") {
      const conflict = stale(one, headers);
      if (conflict) return conflict;
      movement(
        "transfer_in",
        one.fromSite.id,
        one.lines.map((line) => ({ variantId: line.variant.id, to: line.fromLocation?.id, quantity: line.sent })),
      );
      return save({
        ...one,
        status: "cancelled",
        revision: one.revision + 1,
        lines: one.lines.map((line) => ({ ...line, returned: line.sent })),
      });
    }
    return undefined;
  };

  // The lots (manufacturing phase 2 L1b): the list by variant, site and status, a lot's page and its holds.
  const derive = (one: InventoryLot): InventoryLot => {
    const active = one.holds.filter((hold) => !hold.releasedAt);
    const status = active.some((hold) => hold.kind === "block")
      ? "blocked"
      : active.length > 0
        ? "quarantine"
        : "available";
    return { ...one, status, revision: one.revision + 1 };
  };
  const lotRoutes = (method: string, path: string, query: URLSearchParams, body: unknown): Response | undefined => {
    if (path === "/lots" && method === "GET") {
      const variantId = query.get("variantId");
      const status = query.get("status");
      const rows = state.lots
        .filter(
          (one) =>
            (variantId === null || one.variant.id === Number(variantId)) && (status === null || one.status === status),
        )
        .map((one) => ({
          id: one.id,
          number: one.number,
          serial: one.serial,
          variant: one.variant,
          status: one.status,
          expired: one.expired,
          expiry: one.expiry,
          bestBefore: one.bestBefore,
          supplierLot: one.supplierLot,
          onHand: one.onHand,
          receivedAt: one.receivedAt,
        }));
      // A page of `limit` at a time, the cursor the offset of the next.
      const limit = Number(query.get("limit") ?? 100);
      const from = Number(query.get("cursor") ?? 0);
      const more = from + limit < rows.length;
      return jsonResponse(200, {
        lots: rows.slice(from, from + limit),
        ...(more ? { nextCursor: String(from + limit) } : {}),
      });
    }
    // The expiring list's CSV and lot labels (M72, M73): the bytes do not matter here, only that they are asked.
    if (path === "/lots/expiring.csv" && method === "GET")
      return new Response("lot;sku\r\n", { status: 200, headers: { "Content-Type": "text/csv" } });
    if (path === "/lots/labels.pdf" && method === "GET")
      return new Response("%PDF-1.3", { status: 200, headers: { "Content-Type": "application/pdf" } });
    // The expiring list (manufacturing phase 2 L2, M72): lots with stock expiring on or before today + days.
    if (path === "/lots/expiring" && method === "GET") {
      const days = Number(query.get("days") ?? 30);
      const until = new Date(Date.parse(`${meta.today}T00:00:00Z`) + days * 86_400_000).toISOString().slice(0, 10);
      const rows = state.lots
        .filter((one) => one.expiry && one.expiry <= until && one.onHand > 0)
        .sort((a, b) => (a.expiry ?? "").localeCompare(b.expiry ?? ""))
        .map((one) => ({
          id: one.id,
          number: one.number,
          serial: one.serial,
          variant: one.variant,
          status: one.status,
          expired: one.expired,
          expiry: one.expiry,
          bestBefore: one.bestBefore,
          supplierLot: one.supplierLot,
          onHand: one.onHand,
          receivedAt: one.receivedAt,
        }));
      return jsonResponse(200, { lots: rows, more: false, today: meta.today, until });
    }
    // The block with its downstream list (manufacturing phase 2 M92): the lot alone here, a block of the lot page.
    const blocking = /^\/lots\/(\d+)\/block$/.exec(path);
    if (blocking && method === "POST") {
      const index = state.lots.findIndex((one) => one.id === Number(blocking[1]));
      if (index < 0) return new Response(null, { status: 404 });
      const one = state.lots[index];
      const input = body as { cause: InventoryLotHoldInput["cause"]; note?: string };
      if (!one.holds.some((h) => !h.releasedAt && h.kind === "block" && h.source.kind === "lot")) {
        const hold = {
          id: nextId++,
          kind: "block" as const,
          cause: input.cause,
          note: input.note,
          source: { module: "inventory", kind: "lot", id: String(one.id) },
          inventorys: true,
          createdAt: STAMP,
          createdBy: { displayName: "Kari N." },
        };
        state.lots[index] = derive({ ...one, holds: [hold, ...one.holds] });
      }
      const blocked = state.lots[index];
      return jsonResponse(200, {
        lot: blocked,
        blocked: [
          {
            id: blocked.id,
            number: blocked.number,
            sku: blocked.variant.sku,
            productName: blocked.variant.productName,
            root: true,
          },
        ],
      });
    }
    const page = /^\/lots\/(\d+)(\/holds(?:\/(\d+)\/release)?)?$/.exec(path);
    if (!page) return undefined;
    const index = state.lots.findIndex((one) => one.id === Number(page[1]));
    if (index < 0) return new Response(null, { status: 404 });
    const one = state.lots[index];
    if (!page[2] && method === "GET") return jsonResponse(200, one);
    if (page[2] && !page[3] && method === "POST") {
      const input = body as InventoryLotHoldInput;
      if (one.holds.some((h) => !h.releasedAt && h.kind === input.kind && h.inventorys && h.source.kind === "lot"))
        return inventoryProblem(409, "hold_exists");
      const hold = {
        id: nextId++,
        kind: input.kind,
        cause: input.cause,
        note: input.note,
        source: { module: "inventory", kind: "lot", id: String(one.id) },
        inventorys: true,
        createdAt: STAMP,
        createdBy: { displayName: "Kari N." },
      };
      state.lots[index] = derive({ ...one, holds: [hold, ...one.holds] });
      return jsonResponse(200, state.lots[index]);
    }
    if (page[3] && method === "POST") {
      const hold = one.holds.find((h) => h.id === Number(page[3]));
      if (!hold) return new Response(null, { status: 404 });
      if (!hold.inventorys) return inventoryProblem(409, "hold_not_inventorys");
      const note = (body as { note?: string } | undefined)?.note;
      state.lots[index] = derive({
        ...one,
        holds: one.holds.map((h) =>
          h.id === hold.id ? { ...h, releasedAt: STAMP, releasedBy: { displayName: "Kari N." }, releaseNote: note } : h,
        ),
      });
      return jsonResponse(200, state.lots[index]);
    }
    return undefined;
  };

  const answer = (
    method: string,
    path: string,
    query: URLSearchParams,
    body: unknown,
    headers: Headers = new Headers(),
  ): Response => {
    let match: RegExpExecArray | null;
    if (method !== "GET") {
      const custom = server.write?.(method, path, body);
      if (custom) return custom;
    }
    if (path === "/meta") return jsonResponse(200, meta);
    if (path === "/trace" && server.trace) {
      const traced = server.trace(query);
      return traced instanceof Response ? traced : jsonResponse(200, traced);
    }
    // Without a trace of its own, a lot's trace is the lot alone: nothing made from it, nothing delivered.
    if (path === "/trace") {
      const one = state.lots.find((lot) => lot.id === Number(query.get("lotId")));
      if (!one) return new Response(null, { status: 404 });
      return jsonResponse(200, {
        lotId: one.id,
        direction: query.get("direction") ?? "usage",
        components: query.get("components") ?? "tracked",
        maxDepth: 10,
        generatedAt: STAMP,
        deliveries: "shown",
        customerNames: true,
        nodes: [
          {
            key: "0",
            depth: 0,
            kind: "lot",
            variant: one.variant,
            depthLimit: false,
            lot: {
              id: one.id,
              number: one.number,
              serial: one.serial,
              status: one.status,
              quantityIn: one.onHand,
              onHand: one.onHand,
            },
          },
        ],
      });
    }
    if (path === "/trace.csv")
      return new Response("key;depth\r\n", { status: 200, headers: { "Content-Type": "text/csv" } });
    if (path === "/trace.pdf")
      return new Response("%PDF-1.3", { status: 200, headers: { "Content-Type": "application/pdf" } });
    if (path === "/sites" && method === "GET") {
      const status = query.get("status") ?? "active";
      return jsonResponse(200, { sites: state.sites.filter((one) => one.status === status).map(siteFigures) });
    }
    if (path === "/sites" && method === "POST") {
      const input = body as InventorySite;
      const site: InventorySite = {
        ...input,
        fulfilsOrders: input.fulfilsOrders ?? input.kind === "warehouse",
        id: ++nextId,
        status: "active",
        rootLocationId: ++nextId,
        locationCount: 0,
        revision: 1,
        createdAt: STAMP,
        updatedAt: STAMP,
      };
      state.sites.push(site);
      state.locations.push(location(site.rootLocationId, site.id, site.code, "site"));
      return jsonResponse(201, site);
    }
    match = /^\/sites\/(\d+)$/.exec(path);
    if (match) {
      const siteId = Number(match[1]);
      const site = state.sites.find((one) => one.id === siteId);
      return site ? jsonResponse(200, siteFigures(site)) : new Response(null, { status: 404 });
    }
    match = /^\/sites\/(\d+)\/locations$/.exec(path);
    if (match) {
      const siteId = Number(match[1]);
      const status = query.get("status");
      const q = (query.get("q") ?? "").toLowerCase();
      const found = state.locations
        .filter((one) => one.siteId === siteId && (!status || one.status === status))
        .filter((one) => !q || one.fullCode.toLowerCase().includes(q) || (one.name ?? "").toLowerCase().includes(q))
        .sort((a, b) => a.fullCode.localeCompare(b.fullCode));
      return jsonResponse(200, {
        locations: found.slice(0, server.locationLimit ?? 10000),
        truncated: found.length > (server.locationLimit ?? 10000),
      });
    }
    if (path === "/locations" && method === "POST") {
      const input = body as { parentId: number; code: string; kind: InventoryLocation["kind"]; name?: string };
      const parent = locationOf(input.parentId);
      if (!parent) return inventoryProblem(400, "parent_invalid");
      const created = location(
        ++nextId,
        parent.siteId,
        `${parent.fullCode}-${input.code}`,
        input.kind,
        parent.id,
        input.name,
      );
      state.locations.push(created);
      return jsonResponse(201, created);
    }
    match = /^\/locations\/(\d+)$/.exec(path);
    if (match && method === "GET") {
      const one = locationOf(Number(match[1]));
      if (!one) return new Response(null, { status: 404 });
      const path: InventoryLocationRef[] = [];
      for (let up = one.parentId; up !== undefined; up = locationOf(up)?.parentId) {
        const step = locationOf(up);
        if (step) path.unshift(ref(step));
      }
      const site = state.sites.find((s) => s.id === one.siteId) ?? state.sites[0];
      return jsonResponse(200, {
        location: one,
        site: siteRef(site),
        path,
        children: state.locations.filter((child) => child.parentId === one.id),
      });
    }
    match = /^\/locations\/(\d+)\/contents$/.exec(path);
    if (match) {
      const id = Number(match[1]);
      const ids = query.get("subtree") === "false" ? new Set([id]) : below(id);
      return jsonResponse(200, { rows: rowsOf(state.balances.filter((one) => ids.has(one.locationId))) });
    }
    if (path === "/lookup") {
      const q = (query.get("q") ?? "").toUpperCase();
      const token = /^LOC(\d+)$/.exec(q);
      const found = [
        ...state.locations
          .filter((one) => (token ? one.id === Number(token[1]) : one.fullCode === q))
          .map((one) => ({ kind: "location", location: ref(one) })),
        ...catalog
          .filter(
            (one) => one.sku.toUpperCase() === q || one.barcode === q || !!one.packagings?.some((p) => p.barcode === q),
          )
          .map((one) => ({ kind: "variant", variant: one })),
        ...state.transfers
          .filter((one) => q === `TRF-${one.number}`)
          .map((one) => ({ kind: "transfer", transfer: { id: one.id, number: one.number } })),
        // A lot by its label's LOT<id> token or its number.
        ...state.lots
          .filter((one) => q === `LOT${one.id}` || one.number.toUpperCase() === q)
          .map((one) => ({
            kind: "lot",
            lot: { id: one.id, number: one.number, expiry: one.expiry },
            variant: one.variant,
          })),
      ];
      return jsonResponse(200, { matches: found });
    }
    if (path === "/variants") {
      const q = (query.get("q") ?? "").toLowerCase();
      const siteId = Number(query.get("siteId") ?? 0);
      return jsonResponse(200, {
        variants: catalog
          .filter(
            (one) =>
              one.sku.toLowerCase().includes(q) || one.productName.toLowerCase().includes(q) || one.barcode === q,
          )
          .map((one) => {
            const primary = siteId ? placementAt(one.id, siteId)?.primaryLocationId : undefined;
            return primary ? { ...one, primaryLocationId: primary } : one;
          }),
      });
    }
    match = /^\/variants\/(\d+)\/placements\/(\d+)$/.exec(path);
    if (match && (method === "PUT" || method === "DELETE")) {
      const variantId = Number(match[1]);
      const siteId = Number(match[2]);
      const existing = placementAt(variantId, siteId);
      const ifMatch = headers.get("If-Match");
      if (method === "DELETE") {
        if (!existing) return new Response(null, { status: 404 });
        if (ifMatch !== String(existing.revision))
          return inventoryProblem(409, "revision_conflict", { title: "Revision conflict" });
        state.placements = state.placements.filter((one) => one !== existing);
        return new Response(null, { status: 204 });
      }
      const input = body as Omit<Placement, "variantId" | "siteId" | "revision">;
      if (headers.get("If-None-Match") === "*") {
        if (existing) return inventoryProblem(409, "revision_conflict", { title: "Revision conflict" });
      } else if (!existing) return new Response(null, { status: 404 });
      else if (ifMatch !== String(existing.revision))
        return inventoryProblem(409, "revision_conflict", { title: "Revision conflict" });
      if (input.primaryLocationId === null && input.reorderPoint === null && input.maximum === null)
        return inventoryProblem(400, "placement_empty");
      if (input.maximum !== null && input.reorderPoint !== null && input.maximum < input.reorderPoint)
        return inventoryProblem(400, "maximum_below_reorder_point");
      const saved: Placement = { ...input, variantId, siteId, revision: (existing?.revision ?? 0) + 1 };
      state.placements = [...state.placements.filter((one) => one !== existing), saved];
      return jsonResponse(existing ? 200 : 201, placementAnswer(saved, 0, 0) as InventoryPlacement);
    }
    if (path === "/low-stock" || path === "/low-stock.csv") {
      const siteId = Number(query.get("siteId") ?? 0);
      const q = (query.get("q") ?? "").toLowerCase();
      const rows = (server.lowStock ?? []).filter(
        (one) =>
          (!siteId || one.site.id === siteId) &&
          (!q || one.variant.sku.toLowerCase().includes(q) || one.variant.productName.toLowerCase().includes(q)),
      );
      if (path === "/low-stock.csv")
        return new Response(
          ["site,sku,suggested", ...rows.map((one) => `${one.site.code},${one.variant.sku},${one.suggested}`)].join(
            "\n",
          ),
          { status: 200, headers: { "Content-Type": "text/csv" } },
        );
      const size = server.lowStockPageSize ?? 50;
      const from = Number(query.get("cursor") ?? 0);
      const next = from + size < rows.length ? String(from + size) : undefined;
      return jsonResponse(200, { rows: rows.slice(from, from + size), ...(next ? { next } : {}) });
    }
    if (path === "/imports/placements" && method === "POST") {
      const dryRun = query.get("dryRun") !== "false";
      const [header, ...lines] = String(body).trim().split(/\r?\n/);
      if (header.trim() !== "sku,site,primary_location,reorder_point,maximum")
        return inventoryProblem(400, undefined, {
          detail: "The header must be sku,site,primary_location,reorder_point,maximum.",
        });
      const parsed = lines.map((text, index) => {
        const [sku = "", site = "", primaryLocation = "", reorderPoint = "", maximum = ""] = text
          .split(",")
          .map((cell) => cell.trim());
        const where = state.sites.find((one) => one.code === site.toUpperCase());
        const what = catalog.find((one) => one.sku.toUpperCase() === sku.toUpperCase());
        const spot = state.locations.find(
          (one) => one.siteId === where?.id && one.fullCode === primaryLocation.toUpperCase(),
        );
        const outcome: InventoryPlacementImportRow["outcome"] = !what
          ? "variant_not_found"
          : !where
            ? "site_not_found"
            : primaryLocation && !spot
              ? "placement_location_invalid"
              : "ok";
        const row: InventoryPlacementImportRow = {
          row: index + 2,
          sku,
          site,
          primaryLocation,
          reorderPoint,
          maximum,
          outcome,
        };
        return { row, where, what, spot, reorderPoint, maximum };
      });
      const rows = parsed.map((one) => one.row);
      const ok = rows.every((row) => row.outcome === "ok");
      if (dryRun || !ok) return jsonResponse(200, { ok, rows });
      if (importedPlacements.has(String(body))) return jsonResponse(200, { ok, rows });
      importedPlacements.add(String(body));
      for (const one of parsed) {
        const variantId = one.what?.id ?? 0;
        const siteId = one.where?.id ?? 0;
        const existing = placementAt(variantId, siteId);
        state.placements = [
          ...state.placements.filter((p) => p !== existing),
          {
            variantId,
            siteId,
            primaryLocationId: one.spot?.id ?? null,
            reorderPoint: one.reorderPoint === "" ? null : Number(one.reorderPoint),
            maximum: one.maximum === "" ? null : Number(one.maximum),
            revision: (existing?.revision ?? 0) + 1,
          },
        ];
      }
      return jsonResponse(201, { ok, rows, set: parsed.length, cleared: 0 });
    }
    match = /^\/products\/(\d+)\/stock$/.exec(path);
    if (match) {
      const productId = Number(match[1]);
      const fixed = server.productStock?.[productId];
      if (fixed) return jsonResponse(200, fixed);
      return jsonResponse(200, {
        productId,
        variants: catalog.filter((one) => one.productId === productId).map(variantStock),
      });
    }
    match = /^\/variants\/(\d+)\/rules$/.exec(path);
    if (match && method === "GET") {
      const rules = server.rules?.[Number(match[1])] ?? { inspectOnReceipt: false, revision: 0 };
      return jsonResponse(200, { variantId: Number(match[1]), updatedAt: null, ...rules });
    }
    match = /^\/variants\/(\d+)\/stock$/.exec(path);
    if (match) {
      const variant = variantOf(Number(match[1]));
      return variant ? jsonResponse(200, variantStock(variant)) : new Response(null, { status: 404 });
    }
    if (path === "/movements" && method === "GET") {
      const locationId = query.get("locationId");
      const inside = locationId ? below(Number(locationId)) : undefined;
      const rows = state.movements.filter(
        (one) =>
          (!query.get("siteId") || one.site.id === Number(query.get("siteId"))) &&
          (!query.get("kind") || one.kind === query.get("kind")) &&
          (!query.get("variantId") || one.lines.some((line) => line.variant.id === Number(query.get("variantId")))) &&
          (!inside ||
            one.lines.some((line) => (line.from && inside.has(line.from.id)) || (line.to && inside.has(line.to.id)))),
      );
      const limit = Number(query.get("limit") ?? 100);
      const start = Number(query.get("cursor") ?? 0);
      const page = rows.slice(start, start + limit);
      return jsonResponse(200, {
        movements: page.map((one) => ({
          id: one.id,
          kind: one.kind,
          site: one.site,
          postedAt: one.postedAt,
          postedBy: one.postedBy,
          lineCount: one.lines.length,
          ...(one.reason ? { reason: one.reason } : {}),
          ...(one.note ? { note: one.note } : {}),
          ...(one.supplierName ? { supplierName: one.supplierName } : {}),
          ...(one.transfer ? { transfer: one.transfer } : {}),
        })),
        ...(start + limit < rows.length ? { nextCursor: String(start + limit) } : {}),
      });
    }
    match = /^\/movements\/(\d+)$/.exec(path);
    if (match) {
      const movementId = Number(match[1]);
      const one = state.movements.find((m) => m.id === movementId);
      return one ? jsonResponse(200, one) : new Response(null, { status: 404 });
    }
    if (path === "/movements/adjustments" && method === "POST") {
      const input = body as {
        lines: { variantId: number; locationId: number; mode: string; quantity?: number; entered?: Entered }[];
      };
      return posted(
        "adjustment",
        body as Record<string, unknown>,
        input.lines.map((line) => {
          const now = onHandOf(line.variantId, line.locationId);
          const delta = line.mode === "set" ? amount(line) - now : amount(line);
          return delta < 0
            ? { variantId: line.variantId, from: line.locationId, quantity: -delta }
            : { variantId: line.variantId, to: line.locationId, quantity: delta };
        }),
      );
    }
    if (path === "/movements/moves" && method === "POST") {
      const input = body as {
        lines: {
          variantId: number;
          fromLocationId: number;
          toLocationId: number;
          quantity?: number;
          entered?: Entered;
        }[];
      };
      return posted(
        "move",
        body as Record<string, unknown>,
        input.lines.map((line) => ({
          variantId: line.variantId,
          from: line.fromLocationId,
          to: line.toLocationId,
          quantity: amount(line),
          entered: line.entered,
        })),
      );
    }
    if (path === "/movements/receipts" && method === "POST") {
      const input = body as {
        toLocationId: number;
        lines: { variantId: number; locationId?: number; quantity?: number; entered?: Entered }[];
      };
      return posted(
        "receipt",
        body as Record<string, unknown>,
        input.lines.map((line) => ({
          variantId: line.variantId,
          to: line.locationId ?? input.toLocationId,
          quantity: amount(line),
          entered: line.entered,
        })),
      );
    }
    if (path === "/stock") {
      const ids = (query.get("variantIds") ?? "").split(",").map(Number);
      if (ids.length > 100) return inventoryProblem(400, undefined, { detail: "At most 100 ids." });
      return jsonResponse(200, {
        totals: ids
          .map((variantId) => {
            const onHand = state.balances
              .filter((one) => one.variantId === variantId)
              .reduce((sum, one) => sum + one.onHand, 0);
            const reserved = state.reservations
              .filter((one) => one.variantId === variantId)
              .reduce((sum, one) => sum + one.quantity, 0);
            return { variantId, onHand, reserved, available: onHand - reserved, inTransit: 0 };
          })
          .filter((one) => one.onHand !== 0 || one.reserved !== 0),
      });
    }
    if (path === "/counts" && method === "GET") {
      const status = query.get("status");
      const siteId = query.get("siteId");
      return jsonResponse(200, {
        counts: state.counts
          .filter((one) => (!status || one.status === status) && (!siteId || one.site.id === Number(siteId)))
          .map((one) => countAnswer(one, false)),
      });
    }
    if (path === "/counts" && method === "POST") {
      const input = body as { siteId: number; locationId?: number; note?: string };
      const site = state.sites.find((one) => one.id === input.siteId);
      if (!site) return inventoryProblem(400, undefined, { errors: { siteId: ["No such site."] } });
      const scope = locationOf(input.locationId ?? site.rootLocationId);
      if (!scope) return inventoryProblem(400, "location_not_found");
      const inScope = below(scope.id);
      const overlaps = state.counts.some((one) => {
        if (one.status !== "counting" || one.site.id !== site.id) return false;
        return inScope.has(one.scope.id) || below(one.scope.id).has(scope.id);
      });
      if (overlaps) return inventoryProblem(409, "count_scope_overlaps", { title: "Count scope overlaps" });
      const count: InventoryCount = {
        id: ++nextId,
        site: siteRef(site),
        scope: ref(scope),
        status: "counting",
        openedAt: STAMP,
        openedBy: { displayName: "Test User" },
        revision: 1,
        lineCount: 0,
        countedLines: 0,
        movedLines: 0,
        ...(input.note ? { note: input.note } : {}),
        lines: state.balances
          .filter((one) => one.onHand !== 0 && inScope.has(one.locationId))
          .map((one) => ({
            id: ++nextId,
            variant: variantOf(one.variantId) ?? catalog[0],
            location: ref(locationOf(one.locationId) ?? scope),
            expected: one.onHand,
            current: one.onHand,
            added: false,
            moved: false,
          })),
      };
      state.counts.unshift(count);
      return jsonResponse(201, countAnswer(count));
    }
    match = /^\/counts\/(\d+)(\/lines|\/post|\/cancel)?$/.exec(path);
    if (match) {
      const count = state.counts.find((one) => one.id === Number(match?.[1]));
      if (!count) return new Response(null, { status: 404 });
      const action = match[2];
      if (!action) return jsonResponse(200, countAnswer(count));
      if (count.status !== "counting") return inventoryProblem(409, "count_not_open", { title: "Count not open" });
      if (action === "/cancel") {
        count.status = "cancelled";
        count.cancelledAt = STAMP;
        count.cancelledBy = { displayName: "Test User" };
        return jsonResponse(200, countAnswer(count));
      }
      if (action === "/lines") {
        const input = body as {
          lines: {
            variantId: number;
            locationId: number;
            counted?: number;
            entered?: Entered;
            lotId?: number;
            lot?: { number: string };
          }[];
        };
        const scopeIds = below(count.scope.id);
        const refused = input.lines
          .map((line, index) => ({ line, index }))
          .filter(({ line }) => !scopeIds.has(line.locationId))
          .map(({ line, index }) => ({ index, code: "location_not_found", detail: `Location ${line.locationId}.` }));
        if (refused.length > 0) return inventoryProblem(400, undefined, { lines: refused });
        for (const line of input.lines) {
          const counted = line.entered ? amount({ ...line, quantity: undefined }) : (line.counted ?? 0);
          // A lot named by number is found among the variant's or made.
          let lot = line.lotId === undefined ? undefined : state.lots.find((one) => one.id === line.lotId);
          if (line.lot) {
            const number = line.lot.number;
            lot = state.lots.find(
              (one) => one.variant.id === line.variantId && one.number.toLowerCase() === number.toLowerCase(),
            );
            if (!lot) {
              const variant = variantOf(line.variantId) ?? catalog[0];
              lot = {
                id: ++nextId,
                number,
                serial: false,
                variant,
                status: "available",
                expired: false,
                receivedAt: STAMP,
                onHand: 0,
                revision: 1,
                createdAt: STAMP,
                holds: [],
                locations: [],
                movements: [],
                attachments: [],
              };
              state.lots.push(lot);
            }
          }
          let held = count.lines?.find(
            (one) =>
              one.variant.id === line.variantId && one.location.id === line.locationId && one.lot?.id === lot?.id,
          );
          if (!held) {
            const where = locationOf(line.locationId);
            if (!where) continue;
            held = {
              id: ++nextId,
              variant: variantOf(line.variantId) ?? catalog[0],
              location: ref(where),
              ...(lot ? { lot: { id: lot.id, number: lot.number, expiry: lot.expiry } } : {}),
              expected: 0,
              current: 0,
              added: true,
              moved: false,
            } satisfies InventoryCountLine;
            count.lines = [...(count.lines ?? []), held];
          }
          held.counted = counted;
          held.countedBy = { displayName: "Test User" };
          const entered = snapshot(line.variantId, line.entered);
          if (entered) held.entered = entered;
          else delete held.entered;
        }
        return jsonResponse(200, countAnswer(count));
      }
      const input = body as { uncounted: "keep" | "zero"; acceptMoved?: boolean };
      const answered = countAnswer(count);
      const moved = (answered.lines ?? []).map((line, index) => ({ line, index })).filter(({ line }) => line.moved);
      if (moved.length > 0 && !input.acceptMoved)
        return inventoryProblem(409, "count_lines_moved", {
          title: "Count lines moved",
          lines: moved.map(({ line, index }) => ({
            index,
            code: "count_lines_moved",
            detail: `${line.variant.sku} at ${line.location.fullCode}`,
          })),
        });
      const differences = (answered.lines ?? [])
        .map((line) => {
          const counted = line.counted ?? (input.uncounted === "zero" ? 0 : undefined);
          return { line, delta: counted === undefined ? 0 : counted - line.current };
        })
        .filter(({ delta }) => delta !== 0);
      posted(
        "count",
        { siteId: count.site.id, clientRequestId: `count-${count.id}` },
        differences.map(({ line, delta }) =>
          delta < 0
            ? { variantId: line.variant.id, from: line.location.id, quantity: -delta }
            : { variantId: line.variant.id, to: line.location.id, quantity: delta },
        ),
        { countId: count.id },
      );
      count.lines = answered.lines;
      count.status = "posted";
      count.uncounted = input.uncounted;
      count.postedAt = STAMP;
      count.postedBy = { displayName: "Test User" };
      count.movementId = state.movements[0].id;
      return jsonResponse(200, countAnswer(count));
    }
    if (path === "/labels" && method === "POST")
      return new Response(new Blob(["%PDF-1.7 labels"], { type: "application/pdf" }), {
        status: 200,
        headers: { "Content-Type": "application/pdf" },
      });
    if (path === "/imports/opening-balances" && method === "POST") {
      const siteId = Number(query.get("siteId"));
      const dryRun = query.get("dryRun") !== "false";
      const [header, ...lines] = String(body).trim().split(/\r?\n/);
      if (header.trim() !== "location,sku,quantity")
        return inventoryProblem(400, undefined, { detail: "The header must be location,sku,quantity." });
      const parsed = lines.map((text, index) => {
        const [location = "", sku = "", quantity = ""] = text.split(",").map((cell) => cell.trim());
        const where = state.locations.find((one) => one.siteId === siteId && one.fullCode === location.toUpperCase());
        const what = catalog.find((one) => one.sku.toUpperCase() === sku.toUpperCase());
        const n = Number(quantity);
        const outcome: InventoryImportRow["outcome"] = !where
          ? "location_not_found"
          : !what
            ? "variant_not_found"
            : quantity === "" || !Number.isFinite(n) || n <= 0
              ? "quantity_invalid"
              : "ok";
        return { row: { row: index + 2, location, sku, quantity, outcome }, where, what, n };
      });
      const rows: InventoryImportRow[] = parsed.map((one) => one.row);
      const ok = rows.every((row) => row.outcome === "ok");
      if (dryRun || !ok) return jsonResponse(200, { ok, rows });
      posted(
        "adjustment",
        { siteId, clientRequestId: "import", reason: "opening_balance" },
        parsed.map((one) => ({ variantId: one.what?.id ?? 0, to: one.where?.id, quantity: one.n })),
      );
      return jsonResponse(201, { ok, rows, movementId: state.movements[0].id });
    }
    const lot = lotRoutes(method, path, query, body);
    if (lot) return lot;
    const transfer = transferRoutes(method, path, query, body, headers);
    if (transfer) return transfer;
    if (path === "/stats")
      return jsonResponse(200, {
        sites: state.sites.length,
        locations: state.locations.length - state.sites.length,
        stockLines: state.balances.length,
        receiptsThisWeek: state.movements.filter((one) => one.kind === "receipt").length,
        openCounts: 0,
        lotsAwaitingInspection: server.lotsAwaitingInspection ?? 0,
      });
    return new Response(null, { status: 404 });
  };

  const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const prefix = "/api/v1/inventory";
    if (!url.pathname.startsWith(prefix)) return Promise.resolve(new Response(null, { status: 404 }));
    const body = parseBody(init?.body);
    return Promise.resolve(
      answer(
        init?.method ?? "GET",
        url.pathname.slice(prefix.length),
        url.searchParams,
        body,
        new Headers(init?.headers),
      ),
    );
  }) as InventoryStub;
  fetchMock.state = state;
  return fetchMock;
};
