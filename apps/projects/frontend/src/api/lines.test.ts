import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { billingLinesQueryOptions, createBillingLine, updateBillingLine } from "./lines";
import { ApiValidationError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("billingLinesQueryOptions", () => {
  it("reads the project's billing lines", async () => {
    const lines = [{ id: 1, code: "PM", trackableCode: "KVEM1000-PM", variantId: 12, variantMissing: false }];
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, lines)));

    const options = billingLinesQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(lines);
    expect(options.queryKey).toEqual(["projects", "detail", 7, "billing-lines"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/billing-lines", { signal: undefined });
  });

  it("surfaces the 409 the API answers when the products module is off", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(409, { title: "Billing lines need the products module" })));

    const error = await runQuery(billingLinesQueryOptions(7)).catch((e: unknown) => e as { status?: number });

    expect(error).toMatchObject({ status: 409 });
  });
});

describe("createBillingLine", () => {
  it("POSTs the line", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { id: 1 })));
    const input = { code: "PM", variantId: 12, pricingMode: "list" as const };

    await createBillingLine(7, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/billing-lines", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("throws ApiValidationError with the offending field on a 400", async () => {
    stubFetch(() =>
      Promise.resolve(jsonResponse(400, { title: "Invalid billing line", errors: { code: ["Code is already used"] } })),
    );

    const error = await createBillingLine(7, { code: "PM", variantId: 12, pricingMode: "list" }).catch(
      (e: unknown) => e,
    );

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({ code: "Code is already used" });
  });
});

describe("updateBillingLine", () => {
  it("PUTs the line, carrying active only when it should change", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 1 })));

    await updateBillingLine(7, 1, { code: "PM", variantId: 12, pricingMode: "discount", discountPercent: 10 });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/7/billing-lines/1", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: "PM", variantId: 12, pricingMode: "discount", discountPercent: 10 }),
    });

    await updateBillingLine(7, 1, { code: "PM", variantId: 12, pricingMode: "list", active: false });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/7/billing-lines/1", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: "PM", variantId: 12, pricingMode: "list", active: false }),
    });
  });
});
