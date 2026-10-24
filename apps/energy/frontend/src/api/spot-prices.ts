import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * Day-ahead spot prices per price area (energy consumption billing design
 * D2): the intervals the feed worker fetched or a file imported, what the
 * installation holds of them per area, and the file import.
 */

export type SpotPriceSource = "Feed" | "Import";
export type SpotPriceFetchStatus = "ok" | "pending" | "missing" | "failed";

/** One price interval, half-open [start, end), at the resolution its source gave. */
export interface SpotPriceInterval {
  id: number;
  priceArea: string;
  start: string;
  end: string;
  nokPerKwh: number;
  eurPerKwh?: number;
  exchangeRate?: number;
  source: SpotPriceSource;
}

/** The feed worker's record of one area and civil day (`day` is `YYYY-MM-DD`). */
export interface SpotPriceFetch {
  day: string;
  status: SpotPriceFetchStatus;
  attempts: number;
  attemptedAt?: string;
  nextAttemptAt?: string;
  error?: string;
}

/** One area's coverage: `firstStart`/`lastEnd` are absent while the area holds no prices. */
export interface SpotPriceCoverageArea {
  priceArea: string;
  intervalCount: number;
  daysMissingLast30: number;
  firstStart?: string;
  lastEnd?: string;
  lastFetch?: SpotPriceFetch;
}

export interface SpotPriceCoverage {
  areas: SpotPriceCoverageArea[];
}

/** One problem of one row; `column` is the file's 0-based column, absent for the row as a whole. */
export interface SpotPriceImportError {
  row: number;
  column?: number;
  message: string;
}

/** What an import did, or would do, for one area of the file. */
export interface SpotPriceImportArea {
  priceArea: string;
  inserted: number;
  replaced: number;
  unchanged: number;
  rangeStart?: string;
  rangeEnd?: string;
}

/** `errors` lists the first 200 of `errorCount`; `imported` is true only when a run wrote the prices. */
export interface SpotPriceImportResult {
  dryRun: boolean;
  imported: boolean;
  rows: number;
  errorCount: number;
  errors: SpotPriceImportError[];
  areas: SpotPriceImportArea[];
}

/** The server's own cap: a larger file is refused here rather than sent to be refused. */
export const MAX_SPOT_PRICE_IMPORT_FILE_BYTES = 10 * 1024 * 1024;

export const spotPricesQueryKey = ["energy", "spot-prices"] as const;

/** `GET /energy/spot-prices/coverage`: every area in use or already priced. Needs `energy:prices-view`. */
export const spotPriceCoverageQueryOptions = () =>
  queryOptions({
    queryKey: [...spotPricesQueryKey, "coverage"],
    queryFn: ({ signal }) => request<SpotPriceCoverage>("/api/v1/energy/spot-prices/coverage", { signal }),
  });

/**
 * `GET /energy/spot-prices`: one area's intervals starting in [from, to),
 * ascending; `from` and `to` are instants at most 35 days apart. Needs
 * `energy:prices-view`.
 */
export const spotPricesQueryOptions = (area: string, from: string, to: string) =>
  queryOptions({
    queryKey: [...spotPricesQueryKey, "intervals", { area, from, to }],
    queryFn: ({ signal }) =>
      request<SpotPriceInterval[]>(`/api/v1/energy/spot-prices?${new URLSearchParams({ area, from, to })}`, {
        signal,
      }),
  });

/**
 * `POST /energy/spot-prices/import`: the file as the one part named `file`.
 * `dryRun` checks it and writes nothing; `dryRun: false` writes every row in
 * one transaction, or — answered 422 — nothing. Needs `energy:prices-manage`
 * and `energy:prices-view`.
 */
export const importSpotPrices = (file: File, dryRun = true): Promise<SpotPriceImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<SpotPriceImportResult>(`/api/v1/energy/spot-prices/import?dryRun=${dryRun}`, {
    method: "POST",
    body: form,
  });
};
