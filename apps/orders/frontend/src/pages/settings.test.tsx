import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

const url = "/api/v1/orders/settings";

const server = (writes: (call: { method: string }) => Response | undefined = () => undefined, current = settings()) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === url) return jsonResponse(200, current);
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call);
  });

describe("the orders settings", () => {
  it("shows the series with their next numbers, and saves the settings whole with the revision it read", async () => {
    const fake = server((call) =>
      call.method === "PUT" ? jsonResponse(200, settings({ quoteValidityDays: 21, revision: 4 })) : undefined,
    );
    renderRoute("/orders/settings");

    expect(await screen.findByText("The next number is 1043.")).toBeInTheDocument();
    const validity = screen.getByLabelText("Quotes are valid for");
    await userEvent.clear(validity);
    await userEvent.type(validity, "21");
    await userEvent.click(screen.getByRole("combobox", { name: "Send channel" }));
    await userEvent.click(await screen.findByRole("option", { name: "Salg <salg@firma.no>" }));
    await userEvent.click(screen.getByRole("combobox", { name: "Final period when a subscription ends" }));
    await userEvent.click(await screen.findByRole("option", { name: "Pro-rated to the end date" }));
    await userEvent.type(screen.getAllByLabelText("Subject")[0], "Tilbud fra oss");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    expect(fake.sent("PUT", url)?.body).toEqual({
      revision: 3,
      quoteSeriesStart: 1001,
      orderSeriesStart: 5001,
      quoteValidityDays: 21,
      defaultPaymentTermsDays: 14,
      confirmationOnConfirm: false,
      combineByDefault: true,
      subscriptionFinalPeriodDefault: "prorated",
      sendChannelId: "11111111-1111-4111-8111-111111111111",
      defaultSiteId: null,
      freightVariantId: null,
      freightMarkupPercent: 0,
      shippingNoticeOnBook: true,
      mailTexts: [{ kind: "quote", language: "nb", subject: "Tilbud fra oss", body: "" }],
    });
    expect(await screen.findByText("The settings are saved")).toBeInTheDocument();
  });

  it("says that sending is off", async () => {
    server(undefined, settings({ sendingAvailable: false }));
    renderRoute("/orders/settings");
    expect(await screen.findByText(/Sending is off: Communications is not on/)).toBeInTheDocument();
  });

  it("puts a field's refusal on its input", async () => {
    server((call) =>
      call.method === "PUT"
        ? new Response(
            JSON.stringify({ title: "Invalid", status: 400, errors: { quoteSeriesStart: ["Must rise above 1042."] } }),
            {
              status: 400,
              headers: { "Content-Type": "application/problem+json" },
            },
          )
        : undefined,
    );
    renderRoute("/orders/settings");

    await userEvent.click(await screen.findByRole("switch", { name: /Send the order confirmation/ }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findAllByText("Must rise above 1042.")).length).toBeGreaterThan(0);
  });

  it("says a stale save as a conflict", async () => {
    server((call) => (call.method === "PUT" ? refusal(409, "version_conflict") : undefined));
    renderRoute("/orders/settings");

    await userEvent.click(await screen.findByRole("switch", { name: /Combine orders/ }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByTestId("conflict-alert")).toBeInTheDocument();
  });
});
