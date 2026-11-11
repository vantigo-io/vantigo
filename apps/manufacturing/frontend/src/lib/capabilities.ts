import type { ManufacturingMeta } from "../api/meta";

/** Whether the caller may create, change, archive and delete work centers and BoMs: `manufacturing:boms-manage` (§8). */
export const canManageBoms = (meta: ManufacturingMeta | undefined): boolean => meta?.canManageBoms === true;

/**
 * Whether cost fields are shown, and a work center's rates written: the
 * server omits every cost field without `manufacturing:costs-view` and
 * refuses a rate sent without it (`costs_view_required`, M38).
 */
export const canSeeCosts = (meta: ManufacturingMeta | undefined): boolean => meta?.canSeeCosts === true;
