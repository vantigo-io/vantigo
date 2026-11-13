import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingWorkOrderCheck } from "../api/quality";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi, workOrderFixture } from "../test/server";

const URL = "/manufacturing/shop-floor/work-orders/40";

const check = (patch: Partial<ManufacturingWorkOrderCheck>): ManufacturingWorkOrderCheck => ({
  id: 1,
  workOrderId: 40,
  sequence: 1,
  kind: "pass_fail",
  title: "Seal",
  instructions: "",
  nominal: null,
  min: null,
  max: null,
  unit: null,
  frequency: "each_work_order",
  quarantineOnFail: false,
  result: null,
  ...patch,
});

const checks = [
  check({ id: 1, sequence: 1, title: "Seal" }),
  check({
    id: 2,
    sequence: 2,
    kind: "measure",
    title: "Fill weight",
    frequency: "first_piece",
    min: "98",
    max: "102",
    unit: "g",
  }),
  check({ id: 3, sequence: 3, kind: "photo", title: "Photo of the label" }),
];

/** The checks' cards, in the order the panel shows them. */
const cardTitles = () =>
  Array.from(document.querySelectorAll("[data-check-id]")).map((card) => card.getAttribute("data-check-id"));

describe("the work order's Checks panel (manufacturing phase 2 M101)", () => {
  it("shows the first-piece check first, records a pass and a measure with large buttons", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ checks })],
      write: (method, path) =>
        method === "POST" && /\/checks\/\d+\/result$/.test(path)
          ? jsonResponse(200, {
              workOrder: workOrderFixture({ checks }),
              instructions: "",
              components: [],
              openLogs: [],
              myOpenLog: null,
              block: null,
            })
          : undefined,
    });
    renderRoute(URL);
    expect(await screen.findByText("Fill weight")).toBeInTheDocument();
    expect(cardTitles()).toEqual(["2", "1", "3"]);
    expect(screen.getByText("Limits 98 – 102 g")).toBeInTheDocument();

    const seal = document.querySelector('[data-check-id="1"]') as HTMLElement;
    await userEvent.click(within(seal).getByRole("button", { name: "Pass" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/40\/checks\/1\/result$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/checks\/1\/result$/)[0].body).toEqual({ result: "pass" });

    const weight = document.querySelector('[data-check-id="2"]') as HTMLElement;
    const record = within(weight).getByRole("button", { name: "Record" });
    expect(record).toBeDisabled();
    await userEvent.type(within(weight).getByLabelText(/Measured value/), "99,5");
    await userEvent.click(record);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/checks\/2\/result$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/checks\/2\/result$/)[0].body).toEqual({ value: "99.5" });
  });

  it("turns a measure's sign with a button, for a keypad without a minus", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ checks })],
      write: (method, path) =>
        method === "POST" && /\/checks\/\d+\/result$/.test(path)
          ? jsonResponse(200, {
              workOrder: workOrderFixture({ checks }),
              instructions: "",
              components: [],
              openLogs: [],
              myOpenLog: null,
              block: null,
            })
          : undefined,
    });
    renderRoute(URL);
    const weight = (await screen.findByText("Fill weight")).closest("[data-check-id]") as HTMLElement;
    await userEvent.type(within(weight).getByLabelText(/Measured value/), "0,5");
    await userEvent.click(within(weight).getByRole("button", { name: "Plus or minus" }));
    expect(within(weight).getByLabelText(/Measured value/)).toHaveValue("-0,5");
    await userEvent.click(within(weight).getByRole("button", { name: "Record" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/checks\/2\/result$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/checks\/2\/result$/)[0].body).toEqual({ value: "-0.5" });
  });

  it("takes a photo through the camera input and records it with its photo", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrderFixture({ checks })],
      write: (method, path) => {
        if (method === "POST" && path === "/work-orders/40/photos")
          return jsonResponse(201, {
            id: 77,
            workOrderId: 40,
            contentType: "image/png",
            sizeBytes: 3,
            uploadedAt: "2026-11-12T10:00:00Z",
          });
        if (method === "POST" && /\/checks\/3\/result$/.test(path))
          return jsonResponse(200, {
            workOrder: workOrderFixture({ checks }),
            instructions: "",
            components: [],
            openLogs: [],
            myOpenLog: null,
            block: null,
          });
        return undefined;
      },
    });
    renderRoute(URL);
    const photo = (await screen.findByText("Photo of the label")).closest("[data-check-id]") as HTMLElement;
    const input = within(photo).getByLabelText("Take photo", { selector: "input" }) as HTMLInputElement;
    expect(input).toHaveAttribute("capture", "environment");
    await userEvent.upload(input, new File([new Uint8Array([1, 2, 3])], "label.png", { type: "image/png" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/checks\/3\/result$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/checks\/3\/result$/)[0].body).toEqual({ photoId: 77 });
  });

  it("shows a result and offers no buttons to a reader", async () => {
    const recorded = check({
      result: {
        id: 9,
        result: "fail",
        value: null,
        note: "Leaks",
        photoId: null,
        recordedBy: { userId: null, displayName: "Kari" },
        recordedAt: "2026-11-12T10:00:00Z",
        deviationId: 4,
        deviationNumber: "DEV-4",
      },
    });
    stubManufacturingApi({ meta: {}, workOrders: [workOrderFixture({ checks: [recorded] })] });
    renderRoute(URL);
    expect(await screen.findByText("Leaks")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "DEV-4" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pass" })).toBeNull();
  });
});
