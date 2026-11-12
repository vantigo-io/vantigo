import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, INVENTORY_QUERY_KEY, json, queryString, refusalOf, send } from "./request";

type Schemas = components["schemas"];

export type InventoryTrace = Schemas["InventoryTrace"];
export type InventoryTraceNode = Schemas["InventoryTraceNode"];
export type InventoryTraceDirection = Schemas["InventoryTraceDirection"];
export type InventoryTraceComponents = Schemas["InventoryTraceComponents"];
export type InventoryLotBlock = Schemas["InventoryLotBlock"];
export type InventoryLotBlockInput = Schemas["InventoryLotBlockInput"];
export type InventoryLotBlockCause = InventoryLotBlockInput["cause"];

/** A block's causes (manufacturing phase 2 M92). */
export const blockCauses: readonly InventoryLotBlockCause[] = ["recall", "complaint", "other"];

/** What a trace is asked (M89): the lot, the direction and whether the untracked components are listed. */
export interface TraceParams {
  lotId: number;
  direction?: InventoryTraceDirection;
  components?: InventoryTraceComponents;
  maxDepth?: number;
}

const traceQuery = (params: TraceParams) =>
  queryString({
    lotId: params.lotId,
    direction: params.direction,
    components: params.components,
    maxDepth: params.maxDepth,
  });

/** A lot's trace (M87–M90): the tree in order, each node after its parent. */
export const traceQueryOptions = (params: TraceParams) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "trace", params],
    // Read past the shared client, so a refusal keeps its code (trace_too_large).
    queryFn: ({ signal }) =>
      send<InventoryTrace>(`/api/v1/inventory/trace${traceQuery(params)}`, { method: "GET", signal }),
  });

/** The trace as CSV or PDF (M91), with the same parameters. */
export const exportTrace = async (params: TraceParams, format: "csv" | "pdf"): Promise<Blob> => {
  const response = await fetch(appUrl(`/api/v1/inventory/trace.${format}${traceQuery(params)}`), {
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};

/** `POST /lots/{id}/block`: the lot and, with downstream, the lots made from it still in stock (M92). */
export const blockLot = (lotId: number, input: InventoryLotBlockInput) =>
  send<InventoryLotBlock>(`/api/v1/inventory/lots/${lotId}/block`, json("POST", input));

/** What a node shows of a trace: whether it is a lot made from the root still holding stock — a block's downstream list. */
export const downstreamInStock = (trace: InventoryTrace) => {
  const seen = new Set<number>();
  return trace.nodes.filter((node) => {
    if (node.kind !== "lot" || node.depth === 0 || !node.lot || node.lot.onHand <= 0 || seen.has(node.lot.id))
      return false;
    seen.add(node.lot.id);
    return true;
  });
};

/** The deliveries a trace reaches, once each: who received the lot. */
export const tracedDeliveries = (trace: InventoryTrace) => {
  const seen = new Set<number>();
  return trace.nodes.filter((node) => {
    if (node.kind !== "issue" || !node.movement || seen.has(node.movement.id)) return false;
    seen.add(node.movement.id);
    return true;
  });
};
