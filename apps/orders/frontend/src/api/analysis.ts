import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, queryString, request } from "./request";
import type { Backlog, HitRate, Intake, IntakeGroupBy } from "./types";

const base = "/api/v1/orders/analysis";

/** A period's days, both included, as the analysis views take them (O35). */
export interface AnalysisPeriod {
  from: string;
  to: string;
}

const hitRatePath = (period: AnalysisPeriod, salespersonId?: string, format?: "csv") =>
  `${base}/hit-rate${queryString({ ...period, salespersonId, format })}`;
const intakePath = (period: AnalysisPeriod, groupBy: IntakeGroupBy, format?: "csv") =>
  `${base}/intake${queryString({ ...period, groupBy, format })}`;
const backlogPath = (format?: "csv") => `${base}/backlog${queryString({ format })}`;

/** The quotes sent in the period and how they were answered, per salesperson and currency. */
export const hitRateQueryOptions = (period: AnalysisPeriod, salespersonId?: string) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "analysis", "hit-rate", period, salespersonId ?? ""],
    queryFn: ({ signal }) => request<HitRate>(hitRatePath(period, salespersonId), { signal }),
    placeholderData: keepPreviousData,
  });

/** The orders confirmed in the period, by the grouping, per currency. */
export const intakeQueryOptions = (period: AnalysisPeriod, groupBy: IntakeGroupBy) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "analysis", "intake", period, groupBy],
    queryFn: ({ signal }) => request<Intake>(intakePath(period, groupBy), { signal }),
    placeholderData: keepPreviousData,
  });

/** What confirmed orders have not delivered, and have delivered and not invoiced, today. */
export const backlogQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "analysis", "backlog"],
    queryFn: ({ signal }) => request<Backlog>(backlogPath(), { signal }),
  });

/** Where each view's CSV file is, for a download link. */
export const hitRateCsvUrl = (period: AnalysisPeriod, salespersonId?: string) =>
  hitRatePath(period, salespersonId, "csv");
export const intakeCsvUrl = (period: AnalysisPeriod, groupBy: IntakeGroupBy) => intakePath(period, groupBy, "csv");
export const backlogCsvUrl = () => backlogPath("csv");
