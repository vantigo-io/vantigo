import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { timeSettingsQueryOptions, updateTimeSettings } from "./settings";

describe("timeSettingsQueryOptions", () => {
  it("reads the lock date", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { lockedBefore: "2026-09-01" })));

    const options = timeSettingsQueryOptions();
    expect(await runQuery(options)).toEqual({ lockedBefore: "2026-09-01" });
    expect(options.queryKey).toEqual(["time", "settings"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/settings");
  });
});

describe("updateTimeSettings", () => {
  it("sends null to remove the lock", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, {})));

    await updateTimeSettings({ lockedBefore: null });

    expect(sent(fetchMock, "PUT")).toEqual({ url: "/api/v1/time/settings", body: { lockedBefore: null } });
  });
});
