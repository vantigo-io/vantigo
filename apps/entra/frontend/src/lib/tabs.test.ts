import { describe, expect, it } from "vitest";
import { entraTabForPath, entraTabLabelKey, entraTabPath, entraTabs } from "./tabs";

describe("the Entra tabs", () => {
  it("are four, Status first, each under /workspace/entra", () => {
    expect(entraTabs.map((tab) => tab.value)).toEqual(["status", "connection", "accounts", "outlook"]);
    expect(entraTabPath("status")).toBe("/workspace/entra");
    for (const tab of entraTabs.slice(1)) expect(tab.path).toBe(`/workspace/entra/${tab.value}`);
    expect(entraTabLabelKey("outlook")).toBe("tabOutlook");
  });

  it("are told from a location: Status is the address itself and anything that is not another tab's", () => {
    expect(entraTabForPath("/workspace/entra")).toBe("status");
    expect(entraTabForPath("/workspace/entra/")).toBe("status");
    expect(entraTabForPath("/workspace/entra/connection")).toBe("connection");
    expect(entraTabForPath("/workspace/entra/accounts/")).toBe("accounts");
    expect(entraTabForPath("/workspace/entra/outlook")).toBe("outlook");
    expect(entraTabForPath("/workspace/entra/other")).toBe("status");
  });
});
