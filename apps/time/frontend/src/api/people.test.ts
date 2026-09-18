import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery } from "../test/api";
import { stubFetch } from "../test/fetch";
import { peopleOverviewQueryOptions } from "./people";

describe("peopleOverviewQueryOptions", () => {
  it("asks for the number of weeks back", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = peopleOverviewQueryOptions(6);
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "people", 6]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/people?weeks=6");
  });
});
