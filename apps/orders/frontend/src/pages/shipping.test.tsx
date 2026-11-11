import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { CarrierAccount, Delivery, Shipment } from "../api/types";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { meta, settings } from "../test/fixtures";
import { renderRoute, renderShippingSettings } from "../test/render";

// Delivery D (orders design O49–O53): a posted delivery's shipments — made,
// booked, refused in the carrier's words, tracked — and Integrations ›
// Shipping's carrier accounts with their write-only keys.

const deliveryUrl = "/api/v1/orders/deliveries/7001";
const shipmentsUrl = `${deliveryUrl}/shipments`;

const posted: Delivery = {
  id: 7001,
  number: 12,
  orderId: 2001,
  orderNumber: 5001,
  customerId: 42,
  customerName: "Acme AS",
  orderOnHold: false,
  siteId: 1,
  status: "posted",
  packingSlipStored: true,
  version: 2,
  createdAt: "2026-11-05T08:00:00Z",
  updatedAt: "2026-11-05T09:00:00Z",
  postedAt: "2026-11-05T09:00:00Z",
  movementId: 9,
  deliveryAddress: { name: "Acme lager", line1: "Lagerveien 2", postalCode: "2000", city: "Lillestrøm" },
  lines: [],
};

const account: CarrierAccount = {
  id: 1001,
  carrier: "bring",
  carrierAllowed: true,
  name: "Bring",
  customerNumbers: ["5"],
  hasCredentials: true,
  test: true,
  sender: {},
  defaultService: "5800",
  status: "active",
  version: 1,
  createdAt: "2026-11-05T08:00:00Z",
  updatedAt: "2026-11-05T08:00:00Z",
};

const shipment = (overrides: Partial<Shipment> = {}): Shipment => ({
  id: 3001,
  deliveryId: 7001,
  deliveryNumber: 12,
  orderId: 2001,
  orderNumber: 5001,
  customerId: 42,
  carrierAccountId: 1001,
  carrierAccountName: "Bring",
  carrier: "bring",
  test: true,
  service: "5800",
  serviceName: "Parcel to pickup point",
  vas: [],
  status: "draft",
  exception: false,
  voided: false,
  bookedCostEstimate: true,
  recipient: {
    name: "Acme lager",
    line1: "Lagerveien 2",
    line2: "",
    postalCode: "2000",
    city: "Lillestrøm",
    country: "NO",
    contact: "",
    phone: "",
    email: "test@example.invalid",
  },
  packages: [{ position: 1, weightGrams: 1000, lengthMm: 0, widthMm: 0, heightMm: 0, content: "" }],
  events: [],
  label: false,
  waybill: false,
  waybillNeeded: false,
  bookAttempted: false,
  labelInMybringOnly: false,
  version: 1,
  createdAt: "2026-11-05T10:00:00Z",
  updatedAt: "2026-11-05T10:00:00Z",
  ...overrides,
});

describe("a posted delivery's shipments", () => {
  it("ships the delivery and books the shipment under the version it read", async () => {
    let current: Shipment[] = [];
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, posted);
      if (call.method === "GET" && call.url === shipmentsUrl) return jsonResponse(200, { items: current });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(200, { items: [account], carriers: ["bring"] });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts/1001/services")
        return jsonResponse(200, {
          items: [
            {
              code: "5800",
              name: "Parcel to pickup point",
              nameEn: "Parcel to pickup point",
              nameNb: "Pakke til hentested",
              segment: "b2c",
              pickupPoint: true,
              waybill: false,
            },
          ],
        });
      if (call.method === "POST" && call.url === shipmentsUrl) {
        current = [shipment()];
        return jsonResponse(201, current[0]);
      }
      if (call.method === "POST" && call.url === "/api/v1/orders/shipments/3001/book") {
        current = [
          shipment({
            status: "booked",
            consignmentNumber: "70400000000001",
            trackingUrl: "https://tracking.bring.com/tracking/70400000000001",
            label: true,
            version: 2,
          }),
        ];
        return jsonResponse(200, current[0]);
      }
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries/7001");

    const card = await screen.findByTestId("shipments-card");
    await userEvent.click(within(card).getByRole("button", { name: "Ship" }));
    await waitFor(() => expect(fake.sent("POST", shipmentsUrl)?.body).toEqual({}));
    expect(
      await screen.findByText("Test mode: Bring still notifies real phone numbers and e-mails."),
    ).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Book shipment" }));
    await waitFor(() =>
      expect(fake.sent("POST", "/api/v1/orders/shipments/3001/book")?.body).toEqual({
        version: 1,
        expectedUpdatedAt: "2026-11-05T10:00:00Z",
      }),
    );
    expect(await screen.findByText("Shipment booked: 70400000000001")).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Track" })).toHaveAttribute(
      "href",
      "https://tracking.bring.com/tracking/70400000000001",
    );
    expect(screen.getByRole("button", { name: "Label" })).toBeInTheDocument();
    expect(screen.getByText("This service has no separate waybill: the label serves.")).toBeInTheDocument();
  });

  it("says the carrier's refusal in its own words", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, posted);
      if (call.method === "GET" && call.url === shipmentsUrl) return jsonResponse(200, { items: [shipment()] });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(200, { items: [account], carriers: ["bring"] });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts/1001/services")
        return jsonResponse(200, { items: [] });
      if (call.method === "POST" && call.url === "/api/v1/orders/shipments/3001/book")
        return refusal(422, "carrier_refused", { detail: "The recipient's address is incomplete." });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries/7001");

    await userEvent.click(await screen.findByRole("button", { name: "Book shipment" }));
    expect(
      await screen.findByText("The carrier refused the request. The recipient's address is incomplete."),
    ).toBeInTheDocument();
  });

  it("shows a booked shipment's tracking, its exception and that a voided label must not be used", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, posted);
      if (call.method === "GET" && call.url === shipmentsUrl)
        return jsonResponse(200, {
          items: [
            shipment({
              status: "in_transit",
              exception: true,
              consignmentNumber: "70400000000001",
              events: [
                {
                  code: "DEVIATION",
                  text: "Damaged",
                  place: "0150 OSLO",
                  occurredAt: "2026-11-06T10:00:00Z",
                  state: "exception",
                },
              ],
            }),
            shipment({ id: 3002, status: "cancelled", voided: true, consignmentNumber: "70400000000002" }),
          ],
        });
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries/7001");

    expect(await screen.findByText("Exception")).toBeInTheDocument();
    expect(screen.getByTestId("shipment-events")).toHaveTextContent("Damaged · 0150 OSLO");
    expect(screen.getByText("Voided in Vantigo; the label must not be used.")).toBeInTheDocument();
  });

  it("keeps an unresolved booking as it was, and sends a recovered booking's label to Mybring", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === deliveryUrl) return jsonResponse(200, posted);
      if (call.method === "GET" && call.url === shipmentsUrl)
        return jsonResponse(200, {
          items: [
            shipment({ bookAttempted: true }),
            shipment({ id: 3002, status: "booked", consignmentNumber: "70400000000002", labelInMybringOnly: true }),
          ],
        });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(200, { items: [account], carriers: ["bring"] });
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts/1001/services")
        return jsonResponse(200, { items: [] });
      if (call.method === "POST" && call.url === "/api/v1/orders/shipments/3001/book")
        return refusal(409, "booking_outcome_unknown");
      return commonAnswers(call, settings(), meta({ stockKept: true }));
    });
    renderRoute("/orders/deliveries/7001");

    expect(await screen.findByTestId("label-in-mybring")).toHaveTextContent("print the label in Mybring");
    expect(screen.queryByRole("button", { name: "Save shipment" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Book shipment" }));
    expect(await screen.findByText(/Check Mybring, or wait fifteen minutes/)).toBeInTheDocument();
  });
});

describe("Integrations › Shipping", () => {
  it("saves every site's default it shows, a cleared one as null", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(200, { items: [account], carriers: ["bring"] });
      if (call.method === "GET" && call.url === "/api/v1/orders/sites")
        return jsonResponse(200, {
          items: [
            { id: 1, code: "OSL", name: "Oslo", active: true, fulfilsOrders: true },
            { id: 2, code: "BGO", name: "Bergen", active: true, fulfilsOrders: true },
          ],
        });
      if (call.method === "GET" && call.url === "/api/v1/orders/shipping/site-defaults")
        return jsonResponse(200, { items: [{ siteId: 2, accountId: 1001 }] });
      if (call.method === "PUT" && call.url === "/api/v1/orders/shipping/site-defaults")
        return jsonResponse(200, { items: [{ siteId: 1, accountId: 1001 }] });
      return commonAnswers(call, settings(), meta());
    });
    renderShippingSettings();

    const card = await screen.findByTestId("site-defaults");
    await userEvent.click(within(card).getByRole("combobox", { name: "OSL Default account per site" }));
    await userEvent.click(await screen.findByRole("option", { name: "Bring" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(fake.sent("PUT", "/api/v1/orders/shipping/site-defaults")?.body).toEqual({
        items: [
          { siteId: 1, accountId: 1001 },
          { siteId: 2, accountId: 1001 },
        ],
      }),
    );
  });

  it("adds a carrier account with write-only keys and verifies it", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(200, { items: [account], carriers: ["bring"] });
      if (call.method === "GET" && call.url === "/api/v1/orders/sites") return jsonResponse(200, { items: [] });
      if (call.method === "GET" && call.url === "/api/v1/orders/shipping/site-defaults")
        return jsonResponse(200, { items: [] });
      if (call.method === "POST" && call.url === "/api/v1/orders/carrier-accounts")
        return jsonResponse(201, { ...account, id: 1002, name: "Bring cargo" });
      if (call.method === "POST" && call.url === "/api/v1/orders/carrier-accounts/1001/verify")
        return jsonResponse(200, { result: "ok", pricesAvailable: false, notes: [], services: [] });
      return commonAnswers(call, settings(), meta());
    });
    renderShippingSettings();

    const card = await screen.findByTestId("carrier-account");
    expect(within(card).getByText("Test")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Verify" }));
    expect(await screen.findByTestId("verification")).toHaveTextContent(
      "Prices are list prices: the Mybring user lacks the financial role on the customer number.",
    );

    await userEvent.click(screen.getByRole("button", { name: "Add carrier account" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.clear(within(dialog).getByLabelText("Name"));
    await userEvent.type(within(dialog).getByLabelText("Name"), "Bring cargo");
    await userEvent.type(within(dialog).getByRole("combobox", { name: "Customer numbers" }), "7{enter}");
    await userEvent.type(within(dialog).getByLabelText("Mybring user id"), "me@example.com");
    await userEvent.type(within(dialog).getByLabelText("API key"), "secret-key");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(fake.sent("POST", "/api/v1/orders/carrier-accounts")?.body).toMatchObject({
        carrier: "bring",
        name: "Bring cargo",
        customerNumbers: ["7"],
        test: true,
        credentials: { uid: "me@example.com", key: "secret-key" },
      }),
    );
    expect(await screen.findByText("Carrier account saved")).toBeInTheDocument();
  });
});
