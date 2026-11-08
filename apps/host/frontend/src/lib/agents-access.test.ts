import { describe, expect, it } from "vitest";
import { agentsAccessOf } from "./agents-access";

describe("agentsAccessOf", () => {
  it("lets agents:manage manage and leaves the Owner-tier controls to the Owner", () => {
    expect(agentsAccessOf(["agents:view", "agents:manage"])).toEqual({ canManage: true, isOwner: false });
    expect(agentsAccessOf(["agents:view", "agents:manage"], true)).toEqual({ canManage: true, isOwner: true });
    expect(agentsAccessOf(["agents:view"])).toEqual({ canManage: false, isOwner: false });
    expect(agentsAccessOf(undefined)).toEqual({ canManage: false, isOwner: false });
  });
});
