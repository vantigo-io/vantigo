import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, json, refusalOf } from "./request";

type Schemas = components["schemas"];

export type InventoryLabelsInput = Schemas["InventoryLabelsInput"];
export type InventoryLabelLayout = Schemas["InventoryLabelLayout"];

/** The label sheets (I7): Avery L7160, 21 per A4, and L7163, 14 per A4. */
export const labelLayouts: readonly InventoryLabelLayout[] = ["l7160", "l7163"];

/** At most this many labels in one PDF (`labels_too_many`). */
export const MAX_LABELS = 1000;

/** A PDF of location labels (I7), each with its LOC token as a Code 128 barcode, its full code and its name. */
export const printLabels = async (input: InventoryLabelsInput): Promise<Blob> => {
  const response = await fetch(appUrl("/api/v1/inventory/labels"), {
    ...json("POST", input),
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};

/** Hands the person a file: the browser saves it under `name`. */
export const downloadBlob = (blob: Blob, name: string) => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  // The click has handed the file over; the URL is no longer needed.
  setTimeout(() => URL.revokeObjectURL(url), 0);
};
