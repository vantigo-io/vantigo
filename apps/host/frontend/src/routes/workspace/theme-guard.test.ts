import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { describe, expect, it, vi } from "vitest";
import { Route as WorkspaceRoute } from "../workspace";
import { Route as ThemeRoute } from "./theme";

const { fetchSession, getAuthorizationMe } = vi.hoisted(() => ({ fetchSession: vi.fn(), getAuthorizationMe: vi.fn() }));

vi.mock("../../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/auth")>()),
  fetchSession,
}));
vi.mock("../../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/authorization")>()),
  getAuthorizationMe,
}));

type Guarded = { options: { beforeLoad?: unknown } };

const session = (roles: string[]) => ({ user: { id: "u1", displayName: "U", email: "u@x.test", roles } });
const access = (permissions: string[], canManageAuthorization = false) => ({ permissions, canManageAuthorization });

const run = async (route: Guarded) => {
  const beforeLoad = route.options.beforeLoad as (context: unknown) => unknown;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    await beforeLoad({ context: { queryClient }, location: { pathname: "/workspace/theme" } });
  } catch (error) {
    return error;
  }
  return undefined;
};

// B21: the screen is gated by identity:brands, which an Owner holds implicitly (B31).
describe("the brand screen's route", () => {
  for (const route of [ThemeRoute, WorkspaceRoute]) {
    const name = route === ThemeRoute ? "/workspace/theme" : "the workspace area";

    it(`admits an Owner to ${name}`, async () => {
      fetchSession.mockResolvedValue(session(["Owner"]));
      expect(await run(route)).toBeUndefined();
    });

    it(`admits a holder of identity:brands to ${name}`, async () => {
      fetchSession.mockResolvedValue(session(["User"]));
      getAuthorizationMe.mockResolvedValue(access(["identity:brands"]));
      expect(await run(route)).toBeUndefined();
    });

    it(`sends anyone else away from ${name}`, async () => {
      fetchSession.mockResolvedValue(session(["User"]));
      getAuthorizationMe.mockResolvedValue(access(["customers:view"]));
      const thrown = await run(route);
      expect(isRedirect(thrown)).toBe(true);
      expect((thrown as { options?: { to?: string } }).options?.to).toBe("/");
    });
  }
});
