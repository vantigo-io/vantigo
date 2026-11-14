import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import type { ManufacturingWorkOrderCheck } from "../api/quality";
import { request, setStationOperator } from "../api/request";
import { stationOperationList } from "../api/stations";
import { stubFetch } from "../test/fetch";
import { manufacturingProblem, orderFixture, stubManufacturingApi, workOrderFixture } from "../test/server";
import { StationShopFloor } from "./station-floor";

const TOKEN = "operator-token-in-memory";

const photoCheck: ManufacturingWorkOrderCheck = {
  id: 7,
  workOrderId: 41,
  sequence: 1,
  kind: "photo",
  title: "Photo of the joint",
  instructions: "",
  nominal: null,
  min: null,
  max: null,
  unit: null,
  frequency: "each_work_order",
  quarantineOnFail: false,
  result: null,
};

/** A request as `METHOD /path`, the query left off. */
const requestOf = ([input, init]: [RequestInfo | URL, RequestInit | undefined]) =>
  `${init?.method ?? "GET"} ${new URL(String(input), "http://localhost").pathname}`;

/** Whether a request is one of the operations open to stations (M135). */
const allowed = (request: string) =>
  stationOperationList.some(({ method, path }) =>
    new RegExp(`^${method} ${path.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(request),
  );

const renderStation = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <StationShopFloor />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

afterEach(() => setStationOperator(undefined));

describe("the shop floor on a shared station", () => {
  it("makes only requests open to stations, each as the active operator", async () => {
    const last = workOrderFixture({
      id: 41,
      status: "in_progress",
      last: true,
      checks: [photoCheck],
    });
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true, canSeeCosts: false, canManageBoms: false },
      orders: [orderFixture()],
      workOrders: [workOrderFixture({ status: "ready" }), last],
    });
    setStationOperator(() => TOKEN);
    renderStation();

    // The station's work centers, a queue, a work order and every dialog it opens.
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click((await screen.findAllByRole("button", { name: "MO-12 · Cut" }))[0]);
    await userEvent.click(await screen.findByRole("button", { name: "Back" }));
    await userEvent.click((await screen.findAllByRole("button", { name: "MO-12 · Cut" }))[1]);
    for (const action of ["Register production", "Report scrap", "Report a deviation"]) {
      await userEvent.click(await screen.findByRole("button", { name: action }));
      await userEvent.click((await screen.findAllByRole("button", { name: "Cancel" })).at(-1) as HTMLElement);
    }
    await userEvent.click(await screen.findByRole("button", { name: "Start" }));
    await userEvent.click(await screen.findByRole("button", { name: "Back" }));
    await userEvent.click(await screen.findByRole("button", { name: "Block" }));

    await waitFor(() => expect(fetchMock.actualCalls.length).toBeGreaterThan(5));
    const requests = fetchMock.actualCalls.map(requestOf);
    expect(requests.filter((request) => !allowed(request))).toEqual([]);
    for (const [, init] of fetchMock.actualCalls)
      expect(new Headers(init?.headers).get("Authorization")).toBe(`Station ${TOKEN}`);
    expect(requests).toContain("GET /api/v1/manufacturing/orders/5/production-draft");
    expect(requests).toContain("POST /api/v1/manufacturing/work-orders/41/start");
  });

  it("keeps the operator's token out of storage", async () => {
    stubManufacturingApi({ meta: { canWorkShopFloor: true } });
    setStationOperator(() => TOKEN);
    renderStation();
    await screen.findByRole("button", { name: /SAW/ });
    const stored = [
      ...Object.keys(localStorage).map((key) => localStorage.getItem(key) ?? ""),
      ...Object.keys(sessionStorage).map((key) => sessionStorage.getItem(key) ?? ""),
      document.cookie,
    ];
    expect(stored.some((value) => value.includes(TOKEN))).toBe(false);
  });

  it("tells the station when the operator is signed out", async () => {
    let signedOut: string | undefined = "never";
    stubFetch(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: { code: "operator_session_ended", message: "x" } }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    setStationOperator(
      () => TOKEN,
      (code) => {
        signedOut = code;
      },
    );
    renderStation();
    await waitFor(() => expect(signedOut).toBe("operator_session_ended"));
  });

  it("names the center on every start, Start anyway too", async () => {
    const starts: { path: string; body: unknown }[] = [];
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "ready" })],
      write: (method, path, body) => {
        if (method !== "POST" || !path.endsWith("/start")) return undefined;
        starts.push({ path, body });
        return starts.length === 1 ? manufacturingProblem(409, "work_order_pending") : undefined;
      },
    });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click(await screen.findByRole("button", { name: "MO-12 · Cut" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start anyway" }));
    await waitFor(() => expect(starts).toHaveLength(2));
    expect(starts.map(({ body }) => body)).toEqual([{ workCenterId: 1 }, { force: true, workCenterId: 1 }]);
  });

  it("starts a group's work order here through Start anyway, at this center", async () => {
    const starts: { path: string; body: unknown }[] = [];
    const grouped = workOrderFixture({
      id: 42,
      status: "ready",
      workCenter: { id: 2, code: "PAINT", name: "PAINT station", siteId: 1, workingState: "normal" },
    });
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [grouped],
      groupWork: { 1: [grouped] },
      write: (method, path, body) => {
        if (method !== "POST" || !path.endsWith("/start")) return undefined;
        starts.push({ path, body });
        return starts.length === 1 ? manufacturingProblem(409, "work_order_pending") : undefined;
      },
    });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Start here" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start anyway" }));
    await waitFor(() => expect(starts).toHaveLength(2));
    expect(starts).toEqual([
      { path: "/work-orders/42/start", body: { workCenterId: 1 } },
      { path: "/work-orders/42/start", body: { force: true, workCenterId: 1 } },
    ]);
  });

  it("tells the station which operator's request was refused, and which were answered", async () => {
    const refused: (string | undefined)[] = [];
    const answered: string[] = [];
    let token = "first-operator";
    stubFetch((_input: RequestInfo | URL, init?: RequestInit) => {
      const sent = new Headers(init?.headers).get("Authorization");
      // The first operator's session has ended; the second's stands.
      if (sent === "Station first-operator")
        return Promise.resolve(
          new Response(JSON.stringify({ error: { code: "operator_session_ended", message: "x" } }), {
            status: 401,
            headers: { "Content-Type": "application/json" },
          }),
        );
      return Promise.resolve(
        new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    });
    setStationOperator(
      () => token,
      (_code, carried) => refused.push(carried),
      (carried) => answered.push(carried),
    );
    renderStation();
    await waitFor(() => expect(refused).toContain("first-operator"));
    token = "second-operator";
    await expect(request("/api/v1/manufacturing/work-centers")).resolves.toBeDefined();
    expect(answered).toContain("second-operator");
    expect(refused).not.toContain("second-operator");
  });

  it("leaves a signed-out operator's refusal to the station: no failure of its own", async () => {
    let signedOut = false;
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "ready" })],
      write: (method, path) =>
        method === "POST" && path.endsWith("/start") ? manufacturingProblem(401, "operator_session_ended") : undefined,
    });
    setStationOperator(
      () => TOKEN,
      () => {
        signedOut = true;
      },
    );
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click(await screen.findByRole("button", { name: "MO-12 · Cut" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start" }));
    await waitFor(() => expect(signedOut).toBe(true));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.queryByText("Could not do that")).not.toBeInTheDocument();
  });

  it("wraps the actions' labels rather than cutting them off", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "in_progress" })],
    });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    await userEvent.click(await screen.findByRole("button", { name: "MO-12 · Cut" }));
    for (const action of ["Register quantity", "Report a deviation", "Report scrap"]) {
      const label = (await screen.findByRole("button", { name: action })).querySelector(".mantine-Button-label");
      expect(label).toHaveStyle({ whiteSpace: "normal" });
    }
  });

  it("shows a work order's registered quantity, and no remaining work once it is done", async () => {
    const done = workOrderFixture({
      status: "done",
      quantityDone: "4",
      order: { ...workOrderFixture().order, quantity: "4", produced: "0", remaining: "4" },
    });
    // A done work order is no queue's: it is reached here as group work.
    stubManufacturingApi({ meta: { canWorkShopFloor: true }, workOrders: [done], groupWork: { 1: [done] } });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    const card = await screen.findByRole("button", { name: "MO-12 · Cut" });
    expect(card).toHaveTextContent("4 of 4 pcs");
    expect(card).not.toHaveTextContent("remaining");
    await userEvent.click(card);
    expect(await screen.findByText("4 of 4 pcs")).toBeInTheDocument();
    expect(screen.queryByText(/remaining/)).not.toBeInTheDocument();
  });

  it("shows the order's quantity and remaining for an operation that registers none", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "in_progress", last: false, quantityDone: "0" })],
    });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    const card = await screen.findByRole("button", { name: "MO-12 · Cut" });
    expect(card).toHaveTextContent("10 pcs");
    expect(card).not.toHaveTextContent("0 of 10");
    expect(card).toHaveTextContent("10 pcs remaining");
  });

  it("counts what is left from the quantity the work order registered", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ status: "in_progress", quantityDone: "3" })],
    });
    setStationOperator(() => TOKEN);
    renderStation();
    await userEvent.click(await screen.findByRole("button", { name: /SAW/ }));
    const card = await screen.findByRole("button", { name: "MO-12 · Cut" });
    expect(card).toHaveTextContent("3 of 10 pcs");
    expect(card).toHaveTextContent("7 pcs remaining");
    await userEvent.click(card);
    expect(await screen.findByText("3 of 10 pcs · 7 pcs remaining")).toBeInTheDocument();
  });
});
