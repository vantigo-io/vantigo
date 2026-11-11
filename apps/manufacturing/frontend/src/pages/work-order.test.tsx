import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingWorkOrderStatus } from "../api/shop-floor";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import {
  manufacturingProblem,
  orderFixture,
  stubManufacturingApi,
  timeLogFixture,
  workOrderFixture,
} from "../test/server";

const URL = "/manufacturing/shop-floor/work-orders/40";
const myLog = timeLogFixture({ id: 81, endedAt: null, startedAt: new Date(Date.now() - 65_000).toISOString() });

const buttons = () =>
  ["Start", "Pause", "Finish", "Register production", "Report scrap"].filter(
    (name) => screen.queryByRole("button", { name }) !== null,
  );

describe("the work order view", () => {
  it.each<[ManufacturingWorkOrderStatus, boolean, string[]]>([
    ["ready", false, ["Start", "Finish", "Report scrap"]],
    ["pending", false, ["Start", "Report scrap"]],
    ["paused", false, ["Start", "Finish", "Report scrap"]],
    ["in_progress", true, ["Pause", "Finish", "Report scrap"]],
    ["ready", true, ["Pause", "Finish", "Report scrap"]],
    ["done", false, []],
    ["cancelled", false, []],
  ])("offers by status %s (running it: %s): %j", async (status, running, expected) => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status })],
      views: [{ workOrder: workOrderFixture({ status }), myOpenLog: running ? myLog : null }],
    });
    renderRoute(URL);
    expect(await screen.findByText("Others here")).toBeInTheDocument();
    expect(buttons()).toEqual(expected);
  });

  it("offers Register production on the last operation", async () => {
    stubManufacturingApi({ meta: { canManageOrders: true }, workOrders: [workOrderFixture({ last: true })] });
    renderRoute(URL);
    expect(await screen.findByRole("button", { name: "Register production" })).toBeInTheDocument();
  });

  it("offers no action without manufacturing:shop-floor or orders-manage, and says why", async () => {
    stubManufacturingApi({ meta: { canManageBoms: true }, workOrders: [workOrderFixture({ last: true })] });
    renderRoute(URL);
    expect(await screen.findByText(/That needs the permission Work on the shop floor/)).toBeInTheDocument();
    expect(buttons()).toEqual([]);
  });

  it("shows the caller's running timer from its start, and the 12-hour warning", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "in_progress" })],
      views: [
        {
          workOrder: workOrderFixture({ status: "in_progress" }),
          myOpenLog: { ...myLog, startedAt: new Date(Date.now() - 13 * 3_600_000).toISOString() },
        },
      ],
    });
    renderRoute(URL);
    expect(await screen.findByRole("timer")).toHaveTextContent(/^13:00:0\d$/);
    expect(screen.getByText("Running for more than 12 hours")).toBeInTheDocument();
  });

  it("says that starting here stops the caller's log elsewhere", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      views: [{ workOrder: workOrderFixture(), myOpenLog: { ...myLog, workOrderId: 41 } }],
    });
    renderRoute(URL);
    expect(await screen.findByText("You are running another work order")).toBeInTheDocument();
    expect(screen.getByText("Starting here stops your time on the other one.")).toBeInTheDocument();
  });

  it("starts a pending work order anyway with force after work_order_pending", async () => {
    let starts = 0;
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "pending" })],
      write: (method, path) => {
        if (method !== "POST" || path !== "/work-orders/40/start") return undefined;
        starts += 1;
        return starts === 1
          ? manufacturingProblem(409, "work_order_pending")
          : jsonResponse(200, { workOrder: { workOrder: workOrderFixture() }, closedLog: null, production: null });
      },
    });
    renderRoute(URL);
    await userEvent.click(await screen.findByRole("button", { name: "Start" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start anyway" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/start$/)).toHaveLength(2));
    const [first, second] = sent(fetchMock, "POST", /\/work-orders\/40\/start$/).map((call) => call.body);
    expect(first).toEqual({});
    expect(second).toEqual({ force: true });
  });

  it("cannot start at a blocked work center", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      blocks: { 1: { reason: "quality", note: "", by: null, since: "2026-11-11T08:00:00Z" } },
    });
    renderRoute(URL);
    expect(await screen.findByText("Blocked: Quality")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
  });

  it("registers a production on the last operation without finishing it by default", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      orders: [orderFixture()],
      workOrders: [workOrderFixture({ last: true })],
    });
    renderRoute(URL);
    await userEvent.click(await screen.findByRole("button", { name: "Register production" }));
    const dialog = await screen.findByRole("dialog");
    const consumed = await within(dialog).findByLabelText("Consumed of BOARD");
    await waitFor(() => expect(consumed).toHaveValue("40.000"));
    expect(within(dialog).getByRole("checkbox", { name: "Also finish this operation" })).not.toBeChecked();
    expect(within(dialog).getByText(/Finishing stops the time of everyone working on it/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/40\/finish$/)).toHaveLength(0);
  });

  it("finishes the last operation with the production in one request when asked", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      orders: [orderFixture()],
      workOrders: [workOrderFixture({ last: true })],
    });
    renderRoute(URL);
    await userEvent.click(await screen.findByRole("button", { name: "Register production" }));
    const dialog = await screen.findByRole("dialog");
    const consumed = await within(dialog).findByLabelText("Consumed of BOARD");
    await waitFor(() => expect(consumed).toHaveValue("40.000"));
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Also finish this operation" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/finish$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(0);
    const { body } = sent(fetchMock, "POST", /\/work-orders\/40\/finish$/)[0];
    expect(body.production).toMatchObject({ quantity: "10", lines: [{ componentId: 60, consumed: "40.000" }] });
  });

  it("reports scrap with a fresh request id", async () => {
    const component = orderFixture().components[0];
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      views: [{ workOrder: workOrderFixture(), components: [component] }],
    });
    renderRoute(URL);
    await userEvent.click(await screen.findByRole("button", { name: "Report scrap" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Scrap of BOARD"), "2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Report scrap" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/scrap$/)).toHaveLength(1));
    const { body } = sent(fetchMock, "POST", /\/work-orders\/40\/scrap$/)[0];
    expect(body).toMatchObject({ lines: [{ componentId: 60, scrap: "2" }] });
    expect(typeof body.clientRequestId).toBe("string");
  });

  it("renders the instructions as Markdown", async () => {
    stubManufacturingApi({
      views: [{ workOrder: workOrderFixture(), instructions: "## Safety\n\n- Wear **gloves**" }],
    });
    renderRoute(URL);
    expect(await screen.findByRole("heading", { name: "Safety" })).toBeInTheDocument();
    expect(screen.getByText("gloves").tagName).toBe("STRONG");
  });
});
