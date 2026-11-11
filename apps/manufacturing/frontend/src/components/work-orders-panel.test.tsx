import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { manufacturingProblem, stubManufacturingApi, timeLogFixture, workOrderFixture } from "../test/server";

const openTab = async () => {
  await userEvent.click(await screen.findByRole("tab", { name: "Work orders" }));
  return screen.findByText("Time logs");
};

describe("the order's Work orders tab", () => {
  it("lists the work orders with status and minutes, and their time logs", async () => {
    stubManufacturingApi({
      workOrders: [workOrderFixture({ status: "in_progress", loggedMinutes: 90 })],
      timeLogs: [timeLogFixture({ forced: true })],
    });
    renderRoute("/manufacturing/orders/5");
    await openTab();
    expect(screen.getByRole("link", { name: "Cut" })).toBeInTheDocument();
    expect(screen.getByText("In progress")).toBeInTheDocument();
    expect(screen.getByText("90 min / 40 min")).toBeInTheDocument();
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("Out of order")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Correct/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add missing log" })).not.toBeInTheDocument();
  });

  it("checks a correction before sending it: a note, and an end after the start and not in the future", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true }, timeLogs: [timeLogFixture()] });
    renderRoute("/manufacturing/orders/5");
    await openTab();
    await userEvent.click(screen.getByRole("button", { name: /^Correct the log/ }));
    const dialog = await screen.findByRole("dialog");
    const save = within(dialog).getByRole("button", { name: "Correct" });
    await userEvent.click(save);
    expect(within(dialog).getByText("Required")).toBeInTheDocument();

    const ended = within(dialog).getByLabelText("Ended");
    const started = within(dialog).getByLabelText("Started") as HTMLInputElement;
    fireEvent.change(ended, { target: { value: started.value } });
    expect(within(dialog).getByText("The end must be after the start.")).toBeInTheDocument();
    fireEvent.change(ended, { target: { value: "2999-01-01T10:00" } });
    expect(within(dialog).getByText("The end cannot be in the future.")).toBeInTheDocument();
    expect(sent(fetchMock, "PATCH", /\/time-logs\/80$/)).toHaveLength(0);

    fireEvent.change(ended, { target: { value: "2026-11-10T10:00" } });
    await userEvent.type(within(dialog).getByLabelText(/^Why/), "Forgot to pause");
    await userEvent.click(save);
    await waitFor(() => expect(sent(fetchMock, "PATCH", /\/time-logs\/80$/)).toHaveLength(1));
    expect(sent(fetchMock, "PATCH", /\/time-logs\/80$/)[0].body).toMatchObject({
      note: "Forgot to pause",
      endedAt: new Date("2026-11-10T10:00").toISOString(),
      quantity: null,
    });
  });

  it.each([
    ["log_locked", 409, "This log is older than 60 days and can no longer be corrected."],
    ["log_overlaps", 400, "The time overlaps another log of the same person."],
    ["order_closed", 409, "The order is done or cancelled, so its time logs cannot be changed."],
  ])("says the server's %s refusal", async (code, status, words) => {
    stubManufacturingApi({
      meta: { canManageOrders: true },
      timeLogs: [timeLogFixture()],
      write: (method, path) =>
        method === "PATCH" && path === "/time-logs/80" ? manufacturingProblem(status, code) : undefined,
    });
    renderRoute("/manufacturing/orders/5");
    await openTab();
    await userEvent.click(screen.getByRole("button", { name: /^Correct the log/ }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/^Why/), "Fix");
    await userEvent.click(within(dialog).getByRole("button", { name: "Correct" }));
    expect(await within(dialog).findByText(words)).toBeInTheDocument();
  });

  it("adds a missing log for a person on a work order", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/orders/5");
    await openTab();
    await userEvent.click(screen.getByRole("button", { name: "Add missing log" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Person", { selector: "input" }), "Kari");
    await userEvent.click(await screen.findByRole("option", { name: "Kari Nordmann" }));
    fireEvent.change(within(dialog).getByLabelText("Started"), { target: { value: "2026-11-10T08:00" } });
    fireEvent.change(within(dialog).getByLabelText("Ended"), { target: { value: "2026-11-10T09:00" } });
    await userEvent.type(within(dialog).getByLabelText(/^Why/), "Forgot to start");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/time-logs$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/40\/time-logs$/)[0].body).toMatchObject({
      userId: "7d1f2a51-9b0c-4c1e-8a4e-2f3b6c7d8e90",
      kind: "productive",
      note: "Forgot to start",
    });
  });
});
