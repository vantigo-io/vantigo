import { describe, expect, it } from "vitest";
import { workCenterInputOf, workCenterProblems, workCenterUpdateOf, workCenterValuesOf } from "./work-center-body";

const filled = () => ({
  ...workCenterValuesOf(),
  code: "saw1",
  name: "Panel saw",
  siteId: 1,
  setupMinutes: "7,5",
  machineCostPerHour: "450",
  labourCostPerHour: "",
});

describe("a work center's request", () => {
  it("sends the cost rates only to a caller who may see costs, an empty one as null", () => {
    expect(workCenterInputOf(filled(), true)).toMatchObject({
      code: "SAW1",
      setupMinutes: "7.5",
      machineCostPerHour: "450",
      labourCostPerHour: null,
    });
    const without = workCenterInputOf(filled(), false);
    expect("machineCostPerHour" in without).toBe(false);
    expect("labourCostPerHour" in without).toBe(false);
    const update = workCenterUpdateOf(filled(), false, 3);
    expect(update.revision).toBe(3);
    expect("machineCostPerHour" in update).toBe(false);
  });

  it("defaults the capacity, the efficiency and the pattern", () => {
    expect(workCenterInputOf(filled(), false)).toMatchObject({ capacity: 1, timeEfficiency: 100, cleanupMinutes: "0" });
    expect(workCenterInputOf(filled(), false).pattern?.monday).toEqual([{ start: "07:00", end: "15:00" }]);
  });

  it("judges the fields as the server does", () => {
    expect(workCenterProblems(filled(), true)).toEqual({});
    expect(
      workCenterProblems(
        { ...filled(), code: "SAW-1", siteId: null, capacity: 0, timeEfficiency: 501, cleanupMinutes: "-1" },
        false,
      ),
    ).toEqual({
      code: "codeHint",
      siteId: "required",
      capacity: "capacityHint",
      timeEfficiency: "timeEfficiencyHint",
      cleanupMinutes: "notNegative",
    });
    // A rate the caller cannot see is not judged: it is never sent.
    expect(workCenterProblems({ ...filled(), machineCostPerHour: "x" }, false)).toEqual({});
  });
});
