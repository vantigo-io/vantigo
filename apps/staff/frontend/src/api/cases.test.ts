import { describe, expect, it } from "vitest";
import { caseIfMatch, type StaffCase } from "./cases";

const sickCase = (extra: Partial<StaffCase>): StaffCase =>
  ({
    id: 1,
    personId: 2,
    firstDay: "2026-09-07",
    lastDay: "2026-09-11",
    open: true,
    maxBenefitEstimate: "2027-09-06",
    maxBenefitEstimateEdited: false,
    milestones: [],
    ...extra,
  }) as StaffCase;

describe("a case's If-Match", () => {
  it("sends the etag, quoted, when the reader was sent one in place of the revision", () => {
    expect(caseIfMatch(sickCase({ etag: "c1-abc" }))).toBe('"c1-abc"');
  });

  it("sends the revision to the health group's readers", () => {
    expect(caseIfMatch(sickCase({ revision: 3 }))).toBe("3");
  });
});
