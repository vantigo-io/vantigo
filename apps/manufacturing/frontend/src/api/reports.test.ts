import { describe, expect, it } from "vitest";
import { reportPath, reportQueryString } from "./reports";

describe("a report's query", () => {
  it("sends only the filters that are set, leaving the period's default to the server", () => {
    expect(reportQueryString({})).toBe("");
    expect(reportQueryString({ from: "2026-10-01" })).toBe("?from=2026-10-01");
    expect(reportQueryString({ from: "2026-10-01", to: "2026-10-31", siteId: 2 })).toBe(
      "?from=2026-10-01&to=2026-10-31&siteId=2",
    );
  });

  it("asks for the CSV file with format=csv, and never names json", () => {
    expect(reportQueryString({ siteId: 1 }, "csv")).toBe("?siteId=1&format=csv");
    expect(reportPath("utilisation", {}, "json")).toBe("/api/v1/manufacturing/reports/utilisation");
    expect(reportPath("production-cost", { to: "2026-11-11" }, "csv")).toBe(
      "/api/v1/manufacturing/reports/production-cost?to=2026-11-11&format=csv",
    );
  });
});
