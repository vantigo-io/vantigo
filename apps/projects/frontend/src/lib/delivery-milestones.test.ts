import { describe, expect, it } from "vitest";
import {
  deliveryMilestoneStatusColor,
  deliveryMilestoneStatusLabelKey,
  isDeliveryMilestoneStatus,
} from "./delivery-milestones";

describe("deliveryMilestoneStatusLabelKey", () => {
  it("translates open to deliveryMilestoneStatusOpen", () => {
    expect(deliveryMilestoneStatusLabelKey("open")).toBe("deliveryMilestoneStatusOpen");
  });

  it("translates completed to deliveryMilestoneStatusCompleted", () => {
    expect(deliveryMilestoneStatusLabelKey("completed")).toBe("deliveryMilestoneStatusCompleted");
  });
});

describe("deliveryMilestoneStatusColor", () => {
  it("maps status to color", () => {
    expect(deliveryMilestoneStatusColor("open")).toBe("blue");
    expect(deliveryMilestoneStatusColor("completed")).toBe("teal");
  });
});

describe("isDeliveryMilestoneStatus", () => {
  it("identifies known statuses", () => {
    expect(isDeliveryMilestoneStatus("open")).toBe(true);
    expect(isDeliveryMilestoneStatus("completed")).toBe(true);
    expect(isDeliveryMilestoneStatus("invalid")).toBe(false);
  });
});
