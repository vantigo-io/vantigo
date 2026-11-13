import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SCHEDULE_SITE_STORAGE_KEY } from "../lib/schedule-storage";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { manufacturingProblem, scheduleBarFixture, scheduleFixture, stubManufacturingApi } from "../test/server";

afterEach(() => {
  vi.restoreAllMocks();
  Reflect.deleteProperty(document, "elementsFromPoint");
  setTimeZones(null);
  try {
    window.localStorage.removeItem(SCHEDULE_SITE_STORAGE_KEY);
  } catch {
    // storage unavailable: nothing to clear
  }
});

describe("the production schedule", () => {
  it("shows each day's load from the server, the overloaded day marked, and the closed day", async () => {
    stubManufacturingApi();
    renderRoute("/manufacturing/schedule");
    expect(await screen.findByLabelText("Overloaded: 112.5 %")).toBeInTheDocument();
    expect(screen.getByLabelText("Load 50.0 %")).toBeInTheDocument();
    expect(screen.getByText("Holiday")).toBeInTheDocument();
    expect(screen.getByText("SAW")).toBeInTheDocument();
  });

  it("reads the board of the first site, then of the site chosen, a week or two at a time", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/schedule");
    await screen.findByLabelText("Overloaded: 112.5 %");
    const boards = () => sent(fetchMock, "GET", /\/schedule\?/).map((call) => call.url);
    expect(boards().at(-1)).toContain("siteId=1");
    expect(boards().at(-1)).not.toContain("days=");
    await userEvent.click(screen.getByText("Two weeks"));
    await waitFor(() => expect(boards().at(-1)).toContain("days=14"));
    await userEvent.click(screen.getByRole("button", { name: "Later" }));
    await waitFor(() => expect(boards().at(-1)).toContain("from=2026-11-25"));
  });

  it("moves a dragged bar along its row: the reschedule asks for the new start under the work order's revision", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/schedule");
    const bar = await screen.findByRole("button", { name: /^Move MO-12/ });
    // The row's track is 700 px: a column, one day, is 100 px.
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      width: 700,
      height: 64,
      top: 0,
      left: 0,
      right: 700,
      bottom: 64,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    fireEvent.pointerDown(bar, { button: 0, clientX: 30, pointerId: 1 });
    fireEvent.pointerMove(bar, { clientX: 80, pointerId: 1 });
    fireEvent.pointerMove(bar, { clientX: 130, pointerId: 1 });
    fireEvent.pointerUp(bar, { clientX: 130, pointerId: 1 });
    fireEvent.click(bar);

    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)[0].body).toEqual({
      start: "2026-11-12T08:00:00.000Z",
      revision: 3,
    });
    // The drag's own click opens no dialog.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("moves a group work order dropped on another member's row there, looking through the dragged bar", async () => {
    const schedule = scheduleFixture();
    const [saw] = schedule.workCenters;
    saw.bars = [scheduleBarFixture({ memberIds: [2] })];
    schedule.workCenters = [
      saw,
      { ...saw, workCenter: { ...saw.workCenter, id: 2, code: "SAW2", name: "Second saw" }, bars: [] },
    ];
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true }, schedule });
    renderRoute("/manufacturing/schedule");
    const bar = await screen.findByRole("button", { name: /^Move MO-12/ });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      width: 700,
      height: 64,
      top: 0,
      left: 0,
      right: 700,
      bottom: 64,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    // Under the pointer: the bar, which follows it, then the second saw's row.
    const target = document.querySelector('[data-schedule-row="2"]');
    expect(target).not.toBeNull();
    Object.defineProperty(document, "elementsFromPoint", { configurable: true, value: () => [bar, target] });
    fireEvent.pointerDown(bar, { button: 0, clientX: 30, clientY: 10, pointerId: 1 });
    fireEvent.pointerMove(bar, { clientX: 30, clientY: 80, pointerId: 1 });
    fireEvent.pointerUp(bar, { clientX: 30, clientY: 80, pointerId: 1 });
    fireEvent.click(bar);

    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)[0].body).toMatchObject({
      workCenterId: 2,
      revision: 3,
    });
  });

  it("moves a bar through Move to… with a day and a time in the viewer's own zone", async () => {
    setTimeZones({ timeZone: "Asia/Tokyo", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/schedule");
    await userEvent.click(await screen.findByRole("button", { name: /^Move MO-12/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Open MO-12")).toBeInTheDocument();
    fireEvent.change(within(dialog).getByLabelText("Day"), { target: { value: "2026-11-13" } });
    fireEvent.change(within(dialog).getByLabelText("Time"), { target: { value: "07:00" } });
    await userEvent.click(within(dialog).getByRole("button", { name: "Move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/40\/reschedule$/)[0].body).toEqual({
      start: "2026-11-12T22:00:00.000Z",
      revision: 3,
    });
  });

  it("plans an unscheduled order under its revision, and says when it found no capacity", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      write: (method, path) =>
        method === "POST" && path === "/orders/6/plan" ? manufacturingProblem(409, "no_capacity") : undefined,
    });
    renderRoute("/manufacturing/schedule");
    await userEvent.click(await screen.findByRole("button", { name: "Plan MO-13" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/6\/plan$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/6\/plan$/)[0].body).toEqual({ revision: 2 });
    expect(await screen.findByText(/does not fit on its work center's calendar within 365 days/)).toBeInTheDocument();
  });

  it("plans all of the site's orders and lists those that found no capacity", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      write: (method, path) =>
        method === "POST" && path === "/schedule/plan-all"
          ? jsonResponse(200, {
              planned: [],
              noCapacity: [{ id: 6, number: "MO-13", quantity: "4", produced: "0", status: "confirmed" }],
            })
          : undefined,
    });
    renderRoute("/manufacturing/schedule");
    await userEvent.click(await screen.findByRole("button", { name: "Plan all" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/schedule\/plan-all$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/schedule\/plan-all$/)[0].body).toEqual({ siteId: 1 });
    expect(await screen.findByText("0 orders planned")).toBeInTheDocument();
    expect(screen.getByText(/found no capacity within 365 days/)).toBeInTheDocument();
  });

  it("is read-only without orders-manage: no Plan, no Plan all, and a bar leads to its order", async () => {
    stubManufacturingApi({ meta: { canManageOrders: false } });
    const { router } = renderRoute("/manufacturing/schedule");
    await screen.findByLabelText("Overloaded: 112.5 %");
    expect(screen.queryByRole("button", { name: "Plan all" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Plan MO-13" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Move MO-12/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: /^MO-12 · TABLE/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/manufacturing/orders/5"));
  });
});
