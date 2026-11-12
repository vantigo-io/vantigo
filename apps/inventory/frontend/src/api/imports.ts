import type { components } from "../api-schema";
import { queryString, send } from "./request";

type Schemas = components["schemas"];

export type InventoryImportResult = Schemas["InventoryImportResult"];
export type InventoryImportRow = Schemas["InventoryImportRow"];
export type InventoryImportOutcome = Schemas["InventoryImportOutcome"];

/** A row's outcomes (I32), pinned to the contract by i18n.test.ts. */
export const importOutcomes: readonly InventoryImportOutcome[] = [
  "ok",
  "location_not_found",
  "variant_not_found",
  "variant_not_stockable",
  "quantity_invalid",
  "lot_required",
  "lot_not_allowed",
  "lot_invalid",
  "serial_quantity",
];

/**
 * The template's header: the three columns the file must have, then the lot's, which it may (manufacturing
 * phase 2 M71) — a tracked item's row names its lot, an untracked one's leaves them empty.
 */
export const IMPORT_HEADER = "location,sku,quantity,lot,supplier_lot,production_date,best_before,expiry";

/**
 * The opening-balance import (I32): a dry run answers every row's outcome; a
 * commit posts one adjustment of every row, or nothing when a row fails. The
 * same file posted twice to the same site is one movement. The file goes as
 * its bytes: the server reads UTF-8 or Windows-1252, which a browser's
 * decoding would corrupt, and the same bytes are the same replay key.
 */
export const importOpeningBalances = (siteId: number, csv: ArrayBuffer, dryRun: boolean) =>
  send<InventoryImportResult>(`/api/v1/inventory/imports/opening-balances${queryString({ siteId, dryRun })}`, {
    method: "POST",
    headers: { "Content-Type": "text/csv" },
    body: csv,
  });
