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
    revision: 1,
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
    revision: 1,
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

export interface InventoryServer {
  /** Fields of `/meta` over a caller who may do everything. */
  meta?: Partial<InventoryMeta>;
  sites?: InventorySite[];
  locations?: InventoryLocation[];
  variants?: InventoryVariantRef[];
  balances?: Balance[];
  movements?: InventoryMovement[];
  /** Answers `GET /products/{id}/stock` instead of working it out: a Service product's empty answer. */
  productStock?: Record<number, InventoryProductStock>;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

export interface InventoryState {
  sites: InventorySite[];
  locations: InventoryLocation[];
  balances: Balance[];
  movements: InventoryMovement[];
}

export type InventoryStub = StubbedFetch & { state: InventoryState };

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
    movements: server.movements ?? [],
  };
  const catalog = server.variants ?? variants();
  let nextId = 900;

  const meta: InventoryMeta = {
    canViewLocations: true,
    canManageLocations: true,
    canViewStock: true,
    canAdjust: true,
    canReceive: true,
    canCount: false,
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
    const perSite = state.sites
      .map((site) => {
        const here = rows.filter((row) => row.location.siteId === site.id);
        return { site: siteRef(site), onHand: here.reduce((sum, row) => sum + row.onHand, 0), rows: here };
      })
      .filter((one) => one.rows.length > 0);
    return { variant, totalOnHand: rows.reduce((sum, row) => sum + row.onHand, 0), sites: perSite };
  };
  const move = (variantId: number, locationId: number, change: number) => {
    const balance = state.balances.find((one) => one.variantId === variantId && one.locationId === locationId);
    if (balance) balance.onHand += change;
    else state.balances.push({ variantId, locationId, onHand: change });
  };
  const posted = (
    kind: InventoryMovementKind,
    body: Record<string, unknown>,
    lines: { variantId: number; from?: number; to?: number; quantity: number }[],
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
      lines: lines.map((line, index) => {
        const from = line.from === undefined ? undefined : locationOf(line.from);
        const to = line.to === undefined ? undefined : locationOf(line.to);
        if (line.from !== undefined) move(line.variantId, line.from, -line.quantity);
        if (line.to !== undefined) move(line.variantId, line.to, line.quantity);
        return {
          lineNo: index + 1,
          variant: variantOf(line.variantId) ?? catalog[0],
          quantity: line.quantity,
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
      return jsonResponse(200, { sites: state.sites.filter((one) => one.status === status) });
    }
    if (path === "/sites" && method === "POST") {
      const input = body as InventorySite;
      const site: InventorySite = {
        ...input,
        id: ++nextId,
        status: "active",
        rootLocationId: ++nextId,
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
      return site ? jsonResponse(200, site) : new Response(null, { status: 404 });
    }
    match = /^\/sites\/(\d+)\/locations$/.exec(path);
    if (match) {
      const siteId = Number(match[1]);
      const status = query.get("status");
      return jsonResponse(200, {
        locations: state.locations
          .filter((one) => one.siteId === siteId && (!status || one.status === status))
          .sort((a, b) => a.fullCode.localeCompare(b.fullCode)),
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
          .filter((one) => one.sku.toUpperCase() === q || one.barcode === q)
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
      const input = body as { lines: { variantId: number; locationId: number; mode: string; quantity: number }[] };
      return posted(
        "adjustment",
        body as Record<string, unknown>,
        input.lines.map((line) => {
          const now =
            state.balances.find((one) => one.variantId === line.variantId && one.locationId === line.locationId)
              ?.onHand ?? 0;
          const delta = line.mode === "set" ? line.quantity - now : line.quantity;
          return delta < 0
            ? { variantId: line.variantId, from: line.locationId, quantity: -delta }
            : { variantId: line.variantId, to: line.locationId, quantity: delta };
        }),
      );
    }
    if (path === "/movements/moves" && method === "POST") {
      const input = body as {
        lines: { variantId: number; fromLocationId: number; toLocationId: number; quantity: number }[];
      };
      return posted(
        "move",
        body as Record<string, unknown>,
        input.lines.map((line) => ({
          variantId: line.variantId,
          from: line.fromLocationId,
          to: line.toLocationId,
          quantity: line.quantity,
        })),
      );
    }
    if (path === "/movements/receipts" && method === "POST") {
      const input = body as {
        toLocationId: number;
        lines: { variantId: number; locationId?: number; quantity: number }[];
      };
      return posted(
        "receipt",
        body as Record<string, unknown>,
        input.lines.map((line) => ({
          variantId: line.variantId,
          to: line.locationId ?? input.toLocationId,
          quantity: line.quantity,
        })),
      );
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
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    return Promise.resolve(answer(init?.method ?? "GET", url.pathname.slice(prefix.length), url.searchParams, body));
  }) as InventoryStub;
  fetchMock.state = state;
  return fetchMock;
};
