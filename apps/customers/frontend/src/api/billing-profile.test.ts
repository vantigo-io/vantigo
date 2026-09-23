import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  ApiConflictError,
  ApiValidationError,
  checkPeppol,
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
  peppolLookup: null,
  warnings: [],
  groupDefault: null,
};

const registeredLookup = {
  status: "registered",
  canReceiveInvoice: true,
  canReceiveCreditNote: true,
  checkedAt: "2026-09-21T10:00:00Z",
  participantId: "0192:923609016",
  smpHost: "smp.example.test",
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

  it("fills in every field the server leaves out with null", async () => {
    // The server encodes an unset optional field by omitting it
    // (`omitempty` on every nullable field of `CustomerBillingProfile`), so
    // a customer that has decided nothing answers exactly this much.
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: ["no_invoice_address"] }));
    stubFetch(fetchMock);

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual({ ...profile, warnings: ["no_invoice_address"] });
  });

  it("treats an absent warnings list as no warnings", async () => {
    // Go marshals a nil slice as JSON null, so `warnings` can arrive null
    // even though the contract lists it as always present.
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: null })));

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual(profile);
  });

  it('is covered by an invalidateQueries({queryKey: ["customers"]}) prefix — the controller ruling contact info and the customer PUT rely on', () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(customerBillingProfileQueryOptions(1001).queryKey, profile);

    queryClient.invalidateQueries({ queryKey: ["customers"] });

    expect(queryClient.getQueryState(customerBillingProfileQueryOptions(1001).queryKey)?.isInvalidated).toBe(true);
  });

  it("literally reflects the server's body for a customer never checked (no peppolLookup key at all)", async () => {
    // design D3: peppolLookup is only ever present once a lookup was made
    // for the current participant — absent, not null, when there has never
    // been one.
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: [] })));

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual(profile);
  });

  it.each([
    ["carries the group's own term through", { paymentTermsDays: 30 }, 30],
    // A group that decides no term answers its name and nothing else: the
    // inner key is absent, not null, and has to leave this file as null — not
    // undefined, which toStrictEqual tells apart.
    ["fills in a term the group does not decide with null", {}, null],
  ])("groupDefault %s", async (_name, term, expected) => {
    const group = { id: "g1", name: "Retail" };
    stubFetch(
      vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: [], groupDefault: { group, ...term } })),
    );

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toStrictEqual({ ...profile, groupDefault: { group, paymentTermsDays: expected } });
  });

  it("normalises a stored peppolLookup, participantId and smpHost included", async () => {
    stubFetch(
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(200, { revision: 3, warnings: ["ehf_available"], peppolLookup: registeredLookup }),
        ),
    );

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual({ ...profile, warnings: ["ehf_available"], peppolLookup: registeredLookup });
  });

  it("normalises participantId to null when the server withholds it (design D3: no legal-identity-view permission)", async () => {
    const withheld = {
      status: "registered",
      canReceiveInvoice: true,
      canReceiveCreditNote: true,
      checkedAt: "2026-09-21T10:00:00Z",
      smpHost: "smp.example.test",
    };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: [], peppolLookup: withheld })));

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect((result as { peppolLookup: { participantId: string | null } }).peppolLookup.participantId).toBeNull();
  });

  it("normalises smpHost to null when the server leaves it out", async () => {
    const noHost = {
      status: "registered",
      canReceiveInvoice: true,
      canReceiveCreditNote: true,
      checkedAt: "2026-09-21T10:00:00Z",
      participantId: "0192:923609016",
    };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: [], peppolLookup: noHost })));

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect((result as { peppolLookup: { smpHost: string | null } }).peppolLookup.smpHost).toBeNull();
  });
});

describe("checkPeppol", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs the peppol-lookup action and returns the normalised answer", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, registeredLookup));
    stubFetch(fetchMock);

    const result = await checkPeppol(1001);

    expect(result).toEqual(registeredLookup);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/peppol-lookup", { method: "POST" });
  });

  it("normalises a no_identifier answer, which never carries participantId or smpHost", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          status: "no_identifier",
          canReceiveInvoice: false,
          canReceiveCreditNote: false,
          checkedAt: "2026-09-21T10:00:00Z",
        }),
      ),
    );

    const result = await checkPeppol(1001);

    expect(result.participantId).toBeNull();
    expect(result.smpHost).toBeNull();
    expect(result.status).toBe("no_identifier");
  });

  it("throws the generic ApiError shape on a 502, distinguishable by status", async () => {
    stubFetch(
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(502, { title: "Peppol lookup unavailable", status: 502, detail: "Could not reach Peppol." }),
        ),
    );

    const error = await checkPeppol(1001).catch((e: unknown) => e);

    expect((error as { status?: number }).status).toBe(502);
  });

  it("throws the generic ApiError shape on a 503, distinguishable by status", async () => {
    stubFetch(
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(503, { title: "Peppol lookup disabled", status: 503, detail: "Peppol lookup is disabled." }),
        ),
    );

    const error = await checkPeppol(1001).catch((e: unknown) => e);

    expect((error as { status?: number }).status).toBe(503);
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

  it("normalises the saved profile the same way the GET does", async () => {
    // The PUT's 200 body is the same schema, omitted fields and all — and it
    // is written straight into the card's query cache, so it has to arrive
    // in the same full shape or the card would read undefined where it
    // expects null.
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { revision: 4, warnings: [] })));

    const result = await updateBillingProfile(1001, emptyInput);

    expect(result).toEqual({ ...profile, revision: 4 });
  });

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
