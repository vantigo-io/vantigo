import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { postReceipt } from "./movements";
import { setAuthStateClearer, setUnauthorizedHandler } from "./request";

describe("a write sent past the shared client", () => {
  it("clears the session and tells the host on a 401, as the shared client does", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })));
    const order: string[] = [];
    const clearer = vi.fn(() => {
      order.push("clear");
    });
    const handler = vi.fn(() => {
      order.push("unauthorized");
    });
    setAuthStateClearer(clearer);
    setUnauthorizedHandler(handler);
    await expect(
      postReceipt({ clientRequestId: "k", siteId: 1, toLocationId: 12, lines: [{ variantId: 101, quantity: 1 }] }),
    ).rejects.toMatchObject({ status: 401 });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(order).toEqual(["clear", "unauthorized"]);
  });

  it("leaves the session alone on any other refusal", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 403 })));
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    await expect(
      postReceipt({ clientRequestId: "k", siteId: 1, toLocationId: 12, lines: [{ variantId: 101, quantity: 1 }] }),
    ).rejects.toMatchObject({ status: 403, message: "" });
    expect(handler).not.toHaveBeenCalled();
  });
});
