import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  ApiConflictError,
  ApiValidationError,
  customerBillingProfileQueryOptions,
  updateBillingProfile,
} from "./billing-profile";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const profile = {
  invoiceEmail: null,
  reminderEmail: null,
  paymentTermsDays: null,
  currency: null,
  language: null,
  invoiceDelivery: null,
  reminderDelivery: null,
  peppolId: null,
  gln: null,
  buyerReference: null,
  revision: 3,
  warnings: [],
};

describe("customerBillingProfileQueryOptions", () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keys the query ["customers", id, "billing-profile"], the row-revision key a save must invalidate', () => {
    expect(customerBillingProfileQueryOptions(1001).queryKey).toEqual(["customers", 1001, "billing-profile"]);
  });

  it("GETs the customer's billing profile", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, profile));
    stubFetch(fetchMock);

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual(profile);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/billing-profile", expect.anything());
  });

  it('is covered by an invalidateQueries({queryKey: ["customers"]}) prefix — the controller ruling contact info and the customer PUT rely on', () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(customerBillingProfileQueryOptions(1001).queryKey, profile);

    queryClient.invalidateQueries({ queryKey: ["customers"] });

    expect(queryClient.getQueryState(customerBillingProfileQueryOptions(1001).queryKey)?.isInvalidated).toBe(true);
  });
});

describe("updateBillingProfile", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("PUTs the full ten-field profile plus revision", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...profile, invoiceEmail: "invoices@acme.test" }));
    stubFetch(fetchMock);

    const input = {
      invoiceEmail: "invoices@acme.test",
      reminderEmail: null,
      paymentTermsDays: 30,
      currency: "NOK",
      language: "nb",
      invoiceDelivery: "ehf",
      reminderDelivery: "email",
      peppolId: "0192:923609016",
      gln: null,
      buyerReference: null,
      revision: 3,
    };
    const result = await updateBillingProfile(1001, input);

    expect(result).toEqual({ ...profile, invoiceEmail: "invoices@acme.test" });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/billing-profile", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  const emptyInput = {
    invoiceEmail: null,
    reminderEmail: null,
    paymentTermsDays: null,
    currency: null,
    language: null,
    invoiceDelivery: null,
    reminderDelivery: null,
    peppolId: null,
    gln: null,
    buyerReference: null,
    revision: 3,
  };

  it("throws ApiValidationError keyed by field on a 400", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          title: "Invalid billing profile",
          status: 400,
          errors: { currency: ["Currency must be three letters"] },
        }),
      ),
    );

    const error = await updateBillingProfile(1001, { ...emptyInput, currency: "X" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({ currency: "Currency must be three letters" });
  });

  it("throws ApiConflictError with no code on a stale revision", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(409, {
          title: "Customer revision conflict",
          detail: "The customer was changed by someone else.",
          status: 409,
        }),
      ),
    );

    const error = await updateBillingProfile(1001, emptyInput).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect((error as ApiConflictError).code).toBeUndefined();
  });
});
