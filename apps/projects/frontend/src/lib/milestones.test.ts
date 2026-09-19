import { describe, expect, it } from "vitest";
import { isMilestoneStatus, milestoneStatusColor, milestoneStatuses, milestoneStatusLabelKey } from "./milestones";

describe("milestone statuses", () => {
  it("lists them in the order the plan moves through", () => {
    expect(milestoneStatuses).toEqual(["planned", "ready", "invoiced", "cancelled"]);
  });

  it("names each status with its own catalog key", () => {
    expect(milestoneStatusLabelKey("planned")).toBe("milestoneStatusPlanned");
    expect(milestoneStatusLabelKey("ready")).toBe("milestoneStatusReady");
    expect(milestoneStatusLabelKey("invoiced")).toBe("milestoneStatusInvoiced");
    expect(milestoneStatusLabelKey("cancelled")).toBe("milestoneStatusCancelled");
  });

  it("colours ready and invoiced apart from the two quiet statuses", () => {
    expect(milestoneStatusColor("planned")).toBe("gray");
    expect(milestoneStatusColor("ready")).toBe("yellow");
    expect(milestoneStatusColor("invoiced")).toBe("green");
    expect(milestoneStatusColor("cancelled")).toBe("gray");
  });

  it("recognises only the statuses the contract writes", () => {
    expect(isMilestoneStatus("invoiced")).toBe(true);
    expect(isMilestoneStatus("done")).toBe(false);
  });
});
