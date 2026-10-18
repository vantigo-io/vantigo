import { afterEach, describe, expect, it, vi } from "vitest";
import { manageUser, unmanageUser } from "./users";

const respond = () =>
  vi.fn<typeof fetch>(() =>
    Promise.resolve(new Response(JSON.stringify({ id: "u-1" }), { headers: { "Content-Type": "application/json" } })),
  );

afterEach(() => vi.unstubAllGlobals());

describe("adopting a user for Staff through the API", () => {
  it("posts the module and the employee number to the user's manage", async () => {
    const fetch = respond();
    vi.stubGlobal("fetch", fetch);
    await manageUser("u-1", "A-1042");
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/v1/identity/owner/users/u-1/manage");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ module: "staff", employeeNumber: "A-1042" });
  });

  it("posts to the user's unmanage", async () => {
    const fetch = respond();
    vi.stubGlobal("fetch", fetch);
    await unmanageUser("u-1");
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/v1/identity/owner/users/u-1/unmanage");
    expect(init?.method).toBe("POST");
  });
});
