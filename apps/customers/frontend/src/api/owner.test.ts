import { afterEach, describe, expect, it, vi } from "vitest";
import { assignableUsersQueryOptions, setCustomerOwner } from "./owner";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("owner api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("asks for assignable users with the trimmed query, and with no query at all when it is blank", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
      Promise.resolve(jsonResponse([{ userId: "u1", displayName: "Kari Nordmann" }])),
    );
    vi.stubGlobal("fetch", fetchMock);

    await assignableUsersQueryOptions("  kari  ").queryFn?.({} as never);
    await assignableUsersQueryOptions("   ").queryFn?.({} as never);

    const urls = fetchMock.mock.calls.map(([url]) => String(url));
    expect(urls).toEqual(["/api/v1/customers/assignable-users?query=kari", "/api/v1/customers/assignable-users"]);
    // The key is the trimmed term too, so "kari" and " kari " are one cache entry.
    expect(assignableUsersQueryOptions(" kari ").queryKey).toEqual(["customers", "assignable-users", "kari"]);
  });

  it("sends the owner and the revision, and null to clear", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
      Promise.resolve(
        jsonResponse({
          id: 1001,
          customerNumber: 5001,
          name: "Equinor",
          status: "active",
          type: "business",
          createdAt: "2026-06-01T10:00:00Z",
          updatedAt: "2026-07-01T10:00:00Z",
          identity: null,
          revision: 4,
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const saved = await setCustomerOwner(1001, "u1", 3);
    expect(saved.revision).toBe(4);
    // The response goes through the same normalisation every customer read
    // does, so a caller never has to tell an absent owner from a null one.
    expect(saved.owner).toBeNull();
    expect(saved.tags).toEqual([]);

    await setCustomerOwner(1001, null, 4);
    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(String((init as RequestInit).body)));
    expect(bodies).toEqual([
      { ownerUserId: "u1", revision: 3 },
      { ownerUserId: null, revision: 4 },
    ]);
    expect(fetchMock.mock.calls.every(([url]) => String(url) === "/api/v1/customers/1001/owner")).toBe(true);
  });
});
