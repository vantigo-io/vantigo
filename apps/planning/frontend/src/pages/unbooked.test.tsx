import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { stubPlanningApi } from "../test/server";

describe("UnbookedPage — the unbooked list", () => {
  it("lists the people free now first, with their load, free hours and roll-off", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/unbooked");
    const table = await screen.findByRole("table", { name: "Unbooked" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("Grace Hopper");
    expect(rows[0]).toHaveTextContent("Free now");
    expect(rows[1]).toHaveTextContent("Linus Torvalds");
    expect(rows[1]).toHaveTextContent("31%");
    expect(rows[1]).toHaveTextContent("155.5 h");
  });
});
