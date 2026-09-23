import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ApiConflictError, customerRegistryRecordQueryOptions, refreshRegistryRecord } from "./registry";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const problemResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/problem+json" } });

/**
 * Literally what the server sends for the Brønnøysund registry's own entry
 * (`registryRecordResponse` in `apps/server/internal/customers/registry.go`):
 * every optional field is `omitempty`, so `mobile` and `deletedOn` are absent
 * rather than null on a living company that has neither.
 */
const fullRecordBody = {
  organisationNumber: "974760673",
  name: "REGISTERENHETEN I BRØNNØYSUND",
  organisationFormCode: "ORGL",
  organisationForm: "Organisasjonsledd",
  industryCode: "84.110",
  industry: "Generell offentlig administrasjon",
  employees: 487,
  vatRegistered: false,
  bankrupt: false,
  underLiquidation: false,
  underForcedLiquidation: false,
  foundedOn: "1995-08-09",
  website: "www.brreg.no",
  email: "firmapost@brreg.no",
  phone: "75 00 75 09",
  parentOrganisationNumber: "912660680",
  businessAddress: {
    lines: ["Havnegata 48"],
    postalCode: "8900",
    city: "BRØNNØYSUND",
    municipality: "BRØNNØY",
    countryCode: "NO",
  },
  postalAddress: {
    lines: ["Postboks 900"],
    postalCode: "8910",
    city: "BRØNNØYSUND",
    municipality: "BRØNNØY",
    countryCode: "NO",
  },
  fetchedAt: "2026-09-22T09:00:00Z",
};

/** The seven fields the contract requires, and nothing else — the smallest body the server can send. */
const minimalRecordBody = {
  organisationNumber: "923609016",
  name: "EQUINOR ASA",
  vatRegistered: true,
  bankrupt: false,
  underLiquidation: false,
  underForcedLiquidation: false,
  fetchedAt: "2026-09-22T09:00:00Z",
};

const runQuery = (customerId: number) =>
  (customerRegistryRecordQueryOptions(customerId).queryFn as (context: unknown) => Promise<unknown>)({
    signal: undefined,
  });

describe("customerRegistryRecordQueryOptions", () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keys the query ["customers", id, "registry-record"], inside the prefix a legal-identity write invalidates', () => {
    expect(customerRegistryRecordQueryOptions(1001).queryKey).toEqual(["customers", 1001, "registry-record"]);
  });

  it("GETs the customer's registry record", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, fullRecordBody));
    stubFetch(fetchMock);

    const result = await runQuery(1001);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/registry-record", expect.anything());
    expect(result).toEqual({
      ...fullRecordBody,
      deletedOn: null,
      mobile: null,
      registryUpdatedHint: null,
    });
  });

  it("fills in every field the server left out with null", async () => {
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, minimalRecordBody)));

    expect(await runQuery(1001)).toEqual({
      organisationNumber: "923609016",
      name: "EQUINOR ASA",
      organisationFormCode: null,
      organisationForm: null,
      industryCode: null,
      industry: null,
      employees: null,
      vatRegistered: true,
      bankrupt: false,
      underLiquidation: false,
      underForcedLiquidation: false,
      deletedOn: null,
      foundedOn: null,
      website: null,
      email: null,
      phone: null,
      mobile: null,
      parentOrganisationNumber: null,
      businessAddress: null,
      postalAddress: null,
      fetchedAt: "2026-09-22T09:00:00Z",
      registryUpdatedHint: null,
    });
  });

  it("fills in an address's own omitted fields with null, as a foreign address really arrives", async () => {
    // A Polish address carries neither postnummer nor kommune: its postal
    // district is part of the city line instead.
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          ...minimalRecordBody,
          businessAddress: { lines: ["ul. Budowniczych 12"], city: "81-336 GDYNIA", countryCode: "PL" },
        }),
      ),
    );

    expect(((await runQuery(1001)) as { businessAddress: unknown }).businessAddress).toEqual({
      lines: ["ul. Budowniczych 12"],
      postalCode: null,
      city: "81-336 GDYNIA",
      municipality: null,
      countryCode: "PL",
    });
  });

  it("reads a 204 as no record — which is also what a caller without legal-identity-view gets", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 204 })));

    expect(await runQuery(1001)).toBeNull();
  });

  it("carries the feed's hint through, and reports its absence as null", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(jsonResponse(200, { ...fullRecordBody, registryUpdatedHint: "2026-09-22T11:00:00Z" })),
    );
    const withHint = (await runQuery(1001)) as { registryUpdatedHint: string | null };
    expect(withHint?.registryUpdatedHint).toBe("2026-09-22T11:00:00Z");

    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, minimalRecordBody)));
    const without = (await runQuery(1001)) as { registryUpdatedHint: string | null };
    expect(without?.registryUpdatedHint).toBeNull();
  });

  it("reads an address with no countryCode as an empty one, not as undefined", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          ...minimalRecordBody,
          businessAddress: { lines: ["ul. Budowniczych 12"], city: "81-336 GDYNIA" },
        }),
      ),
    );
    const record = (await runQuery(1001)) as { businessAddress: unknown };
    expect(record?.businessAddress).toEqual({
      lines: ["ul. Budowniczych 12"],
      postalCode: null,
      city: "81-336 GDYNIA",
      municipality: null,
      countryCode: "",
    });
  });
});

describe("refreshRegistryRecord", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs and reads back the stored record with what changed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        status: "found",
        record: fullRecordBody,
        changes: [{ field: "name", from: "REGISTERENHETEN", to: "REGISTERENHETEN I BRØNNØYSUND" }],
      }),
    );
    stubFetch(fetchMock);

    const result = await refreshRegistryRecord(1001);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/registry-refresh", { method: "POST" });
    expect(result.status).toBe("found");
    expect(result.record).toEqual({ ...fullRecordBody, deletedOn: null, mobile: null, registryUpdatedHint: null });
    expect(result.changes).toEqual([{ field: "name", from: "REGISTERENHETEN", to: "REGISTERENHETEN I BRØNNØYSUND" }]);
  });

  it("reads a change whose one side the server left out as an empty side", async () => {
    // "from" is omitted when the field was not set before (`registryOptional`).
    const body = { status: "found", record: minimalRecordBody, changes: [{ field: "email", to: "post@acme.test" }] };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, body)));

    const result = await refreshRegistryRecord(1001);

    expect(result.changes).toEqual([{ field: "email", from: null, to: "post@acme.test" }]);
  });

  it("reads an answer that stored no record — removed from open data, or an unknown organisation number", async () => {
    stubFetch(
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(200, { status: "removed", changes: [{ field: "removedFromOpenData", to: "2026-09-01" }] }),
        ),
    );

    const result = await refreshRegistryRecord(1001);

    expect(result.status).toBe("removed");
    expect(result.record).toBeNull();
  });

  it("keeps the 409's code, so the card can tell 'no organisation number' from any other conflict", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        problemResponse(409, {
          type: "about:blank",
          title: "No registry identity",
          detail: "This customer has no Norwegian organisation number.",
          code: "no_registry_identity",
        }),
      ),
    );

    await expect(refreshRegistryRecord(1001)).rejects.toMatchObject({
      name: "ApiConflictError",
      code: "no_registry_identity",
    });
    await expect(refreshRegistryRecord(1001)).rejects.toBeInstanceOf(ApiConflictError);
  });

  it("keeps a 502 a 502, so 'the registry could not be reached' is distinguishable", async () => {
    stubFetch(
      vi
        .fn()
        .mockResolvedValue(
          problemResponse(502, { title: "Registry unavailable", detail: "Brreg could not be reached" }),
        ),
    );

    await expect(refreshRegistryRecord(1001)).rejects.toMatchObject({ status: 502 });
  });
});
