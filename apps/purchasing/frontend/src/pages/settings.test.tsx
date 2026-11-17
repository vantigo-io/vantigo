import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchasingSettings } from "../api/purchasing";
import { fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers } from "../test/fixtures";
import { renderRoute } from "../test/render";

const settings = (): PurchasingSettings => ({
  orderSeriesStart: 3000,
  nextOrderNumber: 3002,
  defaultSiteId: 1,
  sendChannelId: null,
  channels: [
    {
      id: "00000000-0000-4000-8000-0000000000c1",
      address: "innkjop@firma.no",
      displayName: "Innkjøp",
      available: true,
    },
  ],
  sendingAvailable: false,
  orderFooter: "Oppgi ordrenummeret på fakturaen.",
  deliveryAddresses: [],
  priceTolerancePercent: "2",
  revision: 4,
});

const serve = (answer: () => Response) =>
  fakeServer((call) => {
    if (call.url === "/api/v1/purchasing/settings")
      return call.method === "PUT" ? answer() : jsonResponse(200, settings());
    return commonAnswers(call);
  });

describe("the purchasing settings", () => {
  it("saves the series, the channel, the footer, the tolerance and a site's own delivery address", async () => {
    const fake = serve(() => jsonResponse(200, settings()));
    renderRoute("/purchasing/settings");
    expect(await screen.findByText("The next order sent gets number 3002.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("combobox", { name: /^Mail channel/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Innkjøp <innkjop@firma.no>" }));
    const tolerance = screen.getByRole("textbox", { name: /^Price tolerance/ });
    await userEvent.clear(tolerance);
    await userEvent.type(tolerance, "2,5");
    await userEvent.click(screen.getByRole("checkbox", { name: "Butikklager — Own delivery address" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Address" }), "Torget 3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("Saved");
    expect(fake.sent("PUT", "/api/v1/purchasing/settings")?.body).toEqual({
      revision: 4,
      orderSeriesStart: 3000,
      defaultSiteId: 1,
      sendChannelId: "00000000-0000-4000-8000-0000000000c1",
      orderFooter: "Oppgi ordrenummeret på fakturaen.",
      priceTolerancePercent: "2.5",
      deliveryAddresses: [
        {
          siteId: 2,
          address: { name: "Butikklager", line1: "Torget 3", line2: null, postalCode: null, city: null, country: "NO" },
        },
      ],
    });
  });

  it("says a series start that is too low in the reader's words", async () => {
    serve(() => refusal(400, "series_start_too_low"));
    renderRoute("/purchasing/settings");
    await screen.findByText("The next order sent gets number 3002.");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    const alert = await screen.findByRole("alert");
    expect(
      within(alert).getByText("The first order number must be above the highest number used."),
    ).toBeInTheDocument();
  });

  it("offers Load again on a stale revision, which reads the settings afresh", async () => {
    const fake = serve(() => refusal(409, "version_conflict"));
    renderRoute("/purchasing/settings");
    await screen.findByText("The next order sent gets number 3002.");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("Somebody changed it since you opened it. Load it again and repeat your change."),
    ).toBeInTheDocument();
    const reads = fake.count("GET", "/api/v1/purchasing/settings");
    await userEvent.click(screen.getByRole("button", { name: "Load again" }));
    await waitFor(() => expect(fake.count("GET", "/api/v1/purchasing/settings")).toBeGreaterThan(reads));
  });
});
