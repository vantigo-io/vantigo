import type { InventoryCount, InventoryCountLine } from "../api/counts";
import type { InventoryImportRow } from "../api/imports";
import type { InventoryLocation, InventoryLocationRef } from "../api/locations";
import type { InventoryMeta } from "../api/meta";
import type { InventoryMovement, InventoryMovementKind } from "../api/movements";
import type { InventorySite, InventorySiteRef } from "../api/sites";
import type { InventoryProductStock, InventoryVariantRef, InventoryVariantStock } from "../api/stock";
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
  /** How many locations a site's list answers before it says it was truncated; 10,000 as the server's. */
  locationLimit?: number;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

export interface InventoryState {
  sites: InventorySite[];
  locations: InventoryLocation[];
  balances: Balance[];
  reservations: Reservation[];
  movements: InventoryMovement[];
  counts: InventoryCount[];
}

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
  };
  const catalog = server.variants ?? variants();
  let nextId = 900;

  const meta: InventoryMeta = {
    canViewLocations: true,
    canManageLocations: true,
    canViewStock: true,
    canAdjust: true,
    canReceive: true,
    canCount: true,
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
        return { site: siteRef(site), onHand, reserved, available: onHand - reserved, rows: here };
      })
      .filter((one) => one.rows.length > 0 || one.reserved !== 0);
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

  const answer = (method: string, path: string, query: URLSearchParams, body: unknown): Response => {
    let match: RegExpExecArray | null;
    if (method !== "GET") {
      const custom = server.write?.(method, path, body);
      if (custom) return custom;
    }
    if (path === "/meta") return jsonResponse(200, meta);
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
      ];
      return jsonResponse(200, { matches: found });
    }
    if (path === "/variants") {
      const q = (query.get("q") ?? "").toLowerCase();
      return jsonResponse(200, {
        variants: catalog.filter(
          (one) => one.sku.toLowerCase().includes(q) || one.productName.toLowerCase().includes(q) || one.barcode === q,
        ),
      });
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
            return { variantId, onHand, reserved, available: onHand - reserved };
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
          lines: { variantId: number; locationId: number; counted?: number; entered?: Entered }[];
        };
        const scopeIds = below(count.scope.id);
        const refused = input.lines
          .map((line, index) => ({ line, index }))
          .filter(({ line }) => !scopeIds.has(line.locationId))
          .map(({ line, index }) => ({ index, code: "location_not_found", detail: `Location ${line.locationId}.` }));
        if (refused.length > 0) return inventoryProblem(400, undefined, { lines: refused });
        for (const line of input.lines) {
          const counted = line.entered ? amount({ ...line, quantity: undefined }) : (line.counted ?? 0);
          let held = count.lines?.find(
            (one) => one.variant.id === line.variantId && one.location.id === line.locationId,
          );
          if (!held) {
            const where = locationOf(line.locationId);
            if (!where) continue;
            held = {
              id: ++nextId,
              variant: variantOf(line.variantId) ?? catalog[0],
              location: ref(where),
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
    if (path === "/stats")
      return jsonResponse(200, {
        sites: state.sites.length,
        locations: state.locations.length - state.sites.length,
        stockLines: state.balances.length,
        receiptsThisWeek: state.movements.filter((one) => one.kind === "receipt").length,
        openCounts: 0,
      });
    return new Response(null, { status: 404 });
  };

  const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const prefix = "/api/v1/inventory";
    if (!url.pathname.startsWith(prefix)) return Promise.resolve(new Response(null, { status: 404 }));
    const body = parseBody(init?.body);
    return Promise.resolve(answer(init?.method ?? "GET", url.pathname.slice(prefix.length), url.searchParams, body));
  }) as InventoryStub;
  fetchMock.state = state;
  return fetchMock;
};
