import type { ManufacturingDeviation } from "../api/quality";

/** A deviation's status badge colour. */
export const deviationStatusColor = { open: "red", closed: "teal" } as const;

/** The lots a deviation names: the one it was opened from, else those it holds. */
export const deviationLots = (deviation: ManufacturingDeviation): string =>
  deviation.lotNumber ?? deviation.lots.map((lot) => lot.lotNumber).join(", ");
