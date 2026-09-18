import { describe, expect, it } from "vitest";
import { timeCatalog } from "../i18n";
import { isTimeEntryStatus, timeEntryStatusColor, timeEntryStatuses, timeEntryStatusLabelKey } from "./status";

describe("time entry statuses", () => {
  it("lists the five statuses the API knows, in their lifecycle order", () => {
    expect(timeEntryStatuses).toEqual(["draft", "submitted", "approved", "rejected", "invoiced"]);
  });

  it("colours each status", () => {
    expect(timeEntryStatuses.map(timeEntryStatusColor)).toEqual(["gray", "blue", "green", "red", "violet"]);
  });

  it("names each status in both languages", () => {
    for (const status of timeEntryStatuses) {
      const key = timeEntryStatusLabelKey(status) as keyof typeof timeCatalog.en;
      expect(timeCatalog.en[key]).toBeTruthy();
      expect(timeCatalog.nb[key]).toBeTruthy();
    }
  });

  it("recognises only the known statuses", () => {
    expect(isTimeEntryStatus("approved")).toBe(true);
    expect(isTimeEntryStatus("done")).toBe(false);
  });
});
