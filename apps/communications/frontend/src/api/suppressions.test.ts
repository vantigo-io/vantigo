import { describe, expect, it, vi } from "vitest";
import { createSuppression, deleteSuppression, suppressionsQueryOptions } from "./suppressions";

describe("suppression API mapping", () => {
  it("lists suppressions", async () => {
    const response = [{ id: "s-1", emailAddress: "blocked@example.com", reason: "bounce", createdAt: "2026-01-01" }];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const queryFn = suppressionsQueryOptions().queryFn;
    if (!queryFn) throw new Error("suppression query function is required");
    await expect(queryFn({ signal: new AbortController().signal } as never)).resolves.toEqual(response);
  });

  it("adds and deletes a suppression", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "s-1" }), { status: 201 }))
        .mockResolvedValueOnce(new Response(null, { status: 204 })),
    );
    await createSuppression({ emailAddress: "blocked@example.com", reason: "bounce" });
    await deleteSuppression("s-1");
    expect(fetch).toHaveBeenNthCalledWith(
      1,
      "/api/v1/communications/suppressions",
      expect.objectContaining({ method: "POST" }),
    );
    expect(fetch).toHaveBeenNthCalledWith(
      2,
      "/api/v1/communications/suppressions/s-1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });
});
