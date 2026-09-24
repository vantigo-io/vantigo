import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { cancelAnonymisation, downloadPersonalData, scheduleAnonymisation } from "./personal-data";

type Fetch = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

// Literally what the server sends once a day is scheduled: anonymisedAt is
// omitted until the worker has run, and nothing else unset is on the wire.
const scheduled = {
  id: 1005,
  customerNumber: 5,
  name: "Kari Nordmann",
  status: "archived",
  type: "person",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 3 },
  revision: 7,
  anonymisation: { anonymiseOn: "2027-01-31" },
};

describe("personal data api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("schedules with the day in the body and answers the normalised customer", async () => {
    const fetchMock = vi.fn<Fetch>(() => Promise.resolve(json(scheduled)));
    stubFetch(fetchMock);
    const customer = await scheduleAnonymisation(1005, "2027-01-31");
    const call = fetchMock.mock.calls.find(([url]) => String(url) === "/api/v1/customers/1005/anonymisation");
    expect(call?.[1]?.method).toBe("PUT");
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({ anonymiseOn: "2027-01-31" });
    expect(customer.anonymisation).toEqual({ anonymiseOn: "2027-01-31", anonymisedAt: null });
    expect(customer.revision).toBe(7);
  });

  it("cancels with a DELETE and reads the absent anonymisation as null", async () => {
    const cancelled: Record<string, unknown> = { ...scheduled };
    delete cancelled.anonymisation;
    const fetchMock = vi.fn<Fetch>(() => Promise.resolve(json(cancelled)));
    stubFetch(fetchMock);
    expect((await cancelAnonymisation(1005)).anonymisation).toBeNull();
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) => String(url) === "/api/v1/customers/1005/anonymisation" && init?.method === "DELETE",
      ),
    ).toBe(true);
  });

  it("downloads the file under the name the server gave it", async () => {
    stubFetch(
      vi.fn(() =>
        Promise.resolve(
          new Response("{}", {
            status: 200,
            headers: { "Content-Disposition": 'attachment; filename="customer-5-personal-data.json"' },
          }),
        ),
      ),
    );
    expect((await downloadPersonalData({ id: 1005, customerNumber: 5 })).fileName).toBe(
      "customer-5-personal-data.json",
    );
  });
});
