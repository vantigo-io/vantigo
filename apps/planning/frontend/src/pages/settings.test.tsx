import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { settings } from "../test/fixtures";
import { choose, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubPlanningApi } from "../test/server";

describe("PlanningSettingsPage — the settings", () => {
  it("explains the overbooking rule and saves with the revision it read", async () => {
    const fetchMock = stubPlanningApi({ settings: settings({ revision: 3 }) });
    renderRoute("/planning/settings");

    expect(await screen.findByText(/is saved, and the planner is warned about the weeks/)).toBeInTheDocument();
    await choose(document.body, "Overbooking", "Refuse");
    expect(screen.getByText(/so a plan can be mended one row at a time/)).toBeInTheDocument();
    await retype(screen.getByRole("textbox", { name: "Unbooked horizon (weeks)" }), "8");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/planning\/settings$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/planning\/settings$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      overbooking: "refuse",
      defaultLoadUnit: "percent",
      unbookedThresholdPercent: 50,
      unbookedHorizonWeeks: 8,
      requestLeadDays: 14,
      endingLeadWeeks: 4,
    });
    expect(await screen.findByText("The settings are saved")).toBeInTheDocument();
    expect(fetchMock.state.settings.revision).toBe(4);
  });

  it("refuses a horizon out of range before sending it", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/settings");
    await retype(await screen.findByRole("textbox", { name: "Unbooked horizon (weeks)" }), "60");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("A whole number from 1 to 52")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/planning\/settings$/)).toHaveLength(0);
  });
});
