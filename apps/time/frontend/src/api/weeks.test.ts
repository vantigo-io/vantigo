import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { WEEK, week } from "../test/fixtures";
import { submitWeek, weekQueryOptions } from "./weeks";

describe("weekQueryOptions", () => {
  it("reads the caller's week by its Monday", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, week([]))));

    const options = weekQueryOptions(WEEK);
    const result = await runQuery(options);

    expect(result).toEqual(week([]));
    expect(options.queryKey).toEqual(["time", "weeks", WEEK]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/weeks/2026-09-14");
  });
});

describe("submitWeek", () => {
  it("posts to the week's submit endpoint with no body", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, week([]))));

    await submitWeek(WEEK);

    expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/weeks/2026-09-14/submit", body: undefined });
  });
});
