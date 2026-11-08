import { describe, expect, it } from "vitest";
import type { AgentSetupItem } from "../api/types";
import { meetsLevel, permissionGap, permissionTone } from "./levels";

const item = (patch: Partial<AgentSetupItem>): AgentSetupItem => ({
  display: "View customers",
  heldByRoles: true,
  level: "off",
  locked: false,
  needed: "read",
  permissionKey: "customers:view",
  satisfied: false,
  ...patch,
});

describe("the permission levels", () => {
  it("orders off below read below change", () => {
    expect(meetsLevel("off", "read")).toBe(false);
    expect(meetsLevel("read", "read")).toBe(true);
    expect(meetsLevel("read", "change")).toBe(false);
    expect(meetsLevel("change", "read")).toBe(true);
  });

  it("names what is missing", () => {
    expect(permissionGap(item({}))).toEqual({ needsRaise: true, needsRoles: false, locked: false });
    expect(permissionGap(item({ level: "read", heldByRoles: false }))).toEqual({
      needsRaise: false,
      needsRoles: true,
      locked: false,
    });
    expect(permissionGap(item({ locked: true }))).toMatchObject({ locked: true });
  });

  it("tones a satisfied row green, a locked one red and the rest yellow", () => {
    expect(permissionTone(item({ satisfied: true, level: "read" }))).toBe("teal");
    expect(permissionTone(item({ locked: true }))).toBe("red");
    expect(permissionTone(item({}))).toBe("yellow");
  });
});
