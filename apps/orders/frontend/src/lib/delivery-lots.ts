/** One lot a tracked delivery line takes, as the editor holds it (manufacturing phase 2 M77). */
export interface LotPickRow {
  lotId: number;
  locationId: number;
  quantity: number | "";
  allowExpired: boolean;
}

/** What the lots take together, to three decimals. */
export const lotsTotal = (picks: readonly LotPickRow[]): number =>
  Math.round(picks.reduce((sum, pick) => sum + (pick.quantity === "" ? 0 : pick.quantity), 0) * 1000) / 1000;
