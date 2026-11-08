import { describe, expect, it } from "vitest";
import { agentsAccessOf } from "./agents-access";

describe("agentsAccessOf", () => {
  it("lets agents:manage manage and leaves the Owner-tier controls to the Owner", () => {
    expect(agentsAccessOf(["agents:view", "agents:manage"])).toEqual({
      canManage: true,
      isOwner: false,
      canReview: false,
      canSeeActivity: false,
    });
    expect(agentsAccessOf(["agents:view", "agents:manage"], true)).toMatchObject({ canManage: true, isOwner: true });
    expect(agentsAccessOf(["agents:view"])).toMatchObject({ canManage: false, isOwner: false });
    expect(agentsAccessOf(undefined)).toEqual({
      canManage: false,
      isOwner: false,
      canReview: false,
      canSeeActivity: false,
    });
  });

  it("gives the queue to agents:review and the runs to agents:activity, each on its own", () => {
    expect(agentsAccessOf(["agents:review"])).toMatchObject({
      canReview: true,
      canSeeActivity: false,
      canManage: false,
    });
    expect(agentsAccessOf(["agents:activity"])).toMatchObject({ canReview: false, canSeeActivity: true });
  });
});
