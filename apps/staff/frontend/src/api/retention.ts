import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

/** What one run by hand erased, per kind, and what its pass over the deletion ledger attempted. */
export type StaffRetentionRun = Schemas["StaffRetentionRun"];
export type StaffErasedKind = Schemas["StaffErasedKind"];
/** The derived block on `GET /settings`: the ledger's pending and failing rows, the oldest, the last erase. */
export type StaffRetentionState = Schemas["StaffRetentionState"];

/**
 * Runs the retention worker's two steps now (design S15): an erase cycle,
 * with the caller as the erased events' actor, then a ledger pass bounded at
 * thirty seconds. 409 `retention_busy` while the worker holds the lease —
 * nothing ran.
 */
export const runRetention = (): Promise<StaffRetentionRun> =>
  request<StaffRetentionRun>("/api/v1/staff/retention/run", { method: "POST" });
