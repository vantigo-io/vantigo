import { describe, expect, it, vi } from "vitest";
import { sessionClients, setSessionHandlers } from "./session-handlers";

const requestFiles = import.meta.glob<string>("../../../../*/frontend/src/api/request.ts", {
  query: "?raw",
  import: "default",
  eager: true,
});
const packageNames = import.meta.glob<string>("../../../../*/frontend/package.json", { import: "name", eager: true });

/** The directory under apps/ a globbed path is in; Vite writes the host's own paths relative to this file. */
const appOf = (path: string) => /^\.\.\/\.\.\/\.\.\/\.\.\/([^/]+)\//.exec(path)?.[1] ?? "host";

/** Every package under apps/ whose API client lets the host set its 401 handling. */
const packagesWithSessionHandlers = () => {
  const names = new Map(Object.entries(packageNames).map(([path, name]) => [appOf(path), name]));
  return Object.entries(requestFiles)
    .filter(([, source]) => source.includes("setUnauthorizedHandler"))
    .map(([path]) => names.get(appOf(path)));
};

describe("session handlers", () => {
  it("covers every package that builds its own API client", () => {
    expect(Object.keys(sessionClients).sort()).toEqual(packagesWithSessionHandlers().sort());
  });

  it.each(Object.entries(sessionClients))(
    "a 401 from %s clears the session and reaches the host",
    async (_, client) => {
      const order: string[] = [];
      const clearAuthState = vi.fn(() => {
        order.push("clear");
      });
      const onUnauthorized = vi.fn(() => {
        order.push("unauthorized");
      });
      setSessionHandlers({ clearAuthState, onUnauthorized });
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => Response.json({ error: { message: "Not signed in" } }, { status: 401 })),
      );

      await expect(client.request("/api/v1/anything")).rejects.toMatchObject({ status: 401 });

      expect(clearAuthState).toHaveBeenCalledOnce();
      expect(onUnauthorized).toHaveBeenCalledOnce();
      expect(order).toEqual(["clear", "unauthorized"]);
    },
  );

  it("clears every client's handlers", async () => {
    const onUnauthorized = vi.fn();
    setSessionHandlers({ clearAuthState: undefined, onUnauthorized });
    setSessionHandlers(undefined);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({}, { status: 401 })),
    );

    for (const client of Object.values(sessionClients)) {
      await expect(client.request("/api/v1/anything")).rejects.toMatchObject({ status: 401 });
    }
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
