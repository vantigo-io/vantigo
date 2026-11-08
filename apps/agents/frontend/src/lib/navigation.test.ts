import { describe, expect, it } from "vitest";
import { visibleAgentsNavItems } from "./navigation";

describe("the Agents sidebar", () => {
  it("offers the list to agents:view, the queue to agents:review and the settings only to agents:manage", () => {
    expect(visibleAgentsNavItems(["agents:view"]).map((item) => item.key)).toEqual(["agents"]);
    expect(visibleAgentsNavItems(["agents:view", "agents:manage"]).map((item) => item.key)).toEqual([
      "agents",
      "settings",
    ]);
    expect(visibleAgentsNavItems(["agents:manage"]).map((item) => item.key)).toEqual(["settings"]);
    expect(visibleAgentsNavItems(["agents:review"]).map((item) => item.key)).toEqual(["review"]);
    expect(visibleAgentsNavItems(undefined)).toEqual([]);
  });

  it("offers everything to the wildcard", () => {
    expect(visibleAgentsNavItems(["*"])).toHaveLength(3);
  });
});
