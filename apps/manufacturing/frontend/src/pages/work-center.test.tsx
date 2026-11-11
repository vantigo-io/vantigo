import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi, workCenter } from "../test/server";

describe("a work center's form", () => {
  it("shows no cost rates to a caller without costs-view and sends none", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canSeeCosts: false },
      workCenters: [workCenter(1, "SAW", { revision: 5 })],
    });
    renderRoute("/manufacturing/work-centers/1");
    expect(await screen.findByDisplayValue("SAW")).toBeInTheDocument();
    expect(screen.queryByLabelText("Machine cost per hour")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/work-centers\/1$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "PUT", /\/work-centers\/1$/);
    expect(body).toMatchObject({ code: "SAW", revision: 5, siteId: 1 });
    expect(body).not.toHaveProperty("machineCostPerHour");
    expect(body).not.toHaveProperty("labourCostPerHour");
  });

  it("sends the rates a caller with costs-view typed", async () => {
    const fetchMock = stubManufacturingApi({
      workCenters: [workCenter(1, "SAW", { machineCostPerHour: "450", labourCostPerHour: null })],
    });
    renderRoute("/manufacturing/work-centers/1");
    const labour = await screen.findByLabelText("Labour cost per hour");
    await userEvent.type(labour, "620,5");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/work-centers\/1$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/work-centers\/1$/)[0].body).toMatchObject({
      machineCostPerHour: "450",
      labourCostPerHour: "620.5",
    });
  });

  it("refuses to send a pattern whose interval ends before it starts", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/work-centers/1");
    await screen.findByDisplayValue("SAW");
    await userEvent.click(screen.getByRole("combobox", { name: "Monday, interval 1: end" }));
    await userEvent.click(await screen.findByRole("option", { name: "06:00" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("The start must be before the end.")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/work-centers\/1$/)).toHaveLength(0);
  });

  it("adds and removes an interval", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/work-centers/1");
    await screen.findByDisplayValue("SAW");
    await userEvent.click(screen.getByRole("button", { name: "Add an interval on Saturday" }));
    expect(screen.getByRole("combobox", { name: "Saturday, interval 1: start" })).toHaveValue("07:00");
    await userEvent.click(screen.getByRole("button", { name: "Remove interval 1 on Friday" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/work-centers\/1$/)).toHaveLength(1));
    const { pattern } = sent(fetchMock, "PUT", /\/work-centers\/1$/)[0].body as {
      pattern: Record<string, unknown[]>;
    };
    expect(pattern.saturday).toEqual([{ start: "07:00", end: "15:00" }]);
    expect(pattern.friday).toEqual([]);
  });
});
