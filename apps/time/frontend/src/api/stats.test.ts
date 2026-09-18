import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery } from "../test/api";
import { stubFetch } from "../test/fetch";
import {
  projectTimeSummaryQueryOptions,
  timeStatsAttentionQueryOptions,
  timeStatsSummaryQueryOptions,
  timeStatsTimeseriesQueryOptions,
} from "./stats";

describe("stats queries", () => {
  it("reads the summary for a period", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, {})));

    await runQuery(timeStatsSummaryQueryOptions({ from: "2026-09-01T00:00:00Z", to: "2026-09-18T00:00:00Z" }));

    expect(fetchMock.actualCalls[0]?.[0]).toBe(
      "/api/v1/time/stats/summary?from=2026-09-01T00%3A00%3A00Z&to=2026-09-18T00%3A00%3A00Z",
    );
  });

  it("reads the summary for the server's default period", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, {})));

    await runQuery(timeStatsSummaryQueryOptions());

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/stats/summary");
  });

  it("reads one metric's daily series", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = timeStatsTimeseriesQueryOptions("billableHours");
    await runQuery(options);

    expect(options.queryKey[0]).toBe("time");
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/stats/timeseries?metric=billableHours");
  });

  it("reads the attention items", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));
    await runQuery(timeStatsAttentionQueryOptions());
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/stats/attention");
  });

  it("reads a project's hours summary", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, {})));

    const options = projectTimeSummaryQueryOptions(1001);
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "project-summary", 1001]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/projects/1001/summary");
  });
});
