import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Route as IndexRoute } from "./index";
import { Route as WorkspaceRoute } from "./workspace";
import { Route as WorkspaceInvitationsRoute } from "./workspace/invitations";
import { Route as WorkspaceOverviewRoute } from "./workspace/overview";
import { Route as WorkspaceRolesRoute } from "./workspace/roles";
import { Route as WorkspaceUsersRoute } from "./workspace/users";

const { fetchSession, getAuthorizationMe } = vi.hoisted(() => ({
  fetchSession: vi.fn(),
  getAuthorizationMe: vi.fn(),
}));

vi.mock("../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/auth")>()),
  fetchSession,
}));

vi.mock("../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/authorization")>()),
  getAuthorizationMe,
}));

type GuardedRoute = { options: { beforeLoad?: unknown } };

const sessionWithRoles = (roles: string[], isSystemAdmin = false) => ({
  user: { id: "user-1", displayName: "Test User", email: "test@vantigo.test", roles },
  isSystemAdmin,
});

/** Drives a route's `beforeLoad` guard directly; returns whatever it throws. */
const runGuard = async (route: GuardedRoute) => {
  const beforeLoad = route.options.beforeLoad as ((context: unknown) => unknown) | undefined;
  if (!beforeLoad) throw new Error("the route declares no beforeLoad guard");
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    await beforeLoad({ context: { queryClient }, location: { pathname: "/workspace" } });
  } catch (error) {
    return error;
  }
  return undefined;
};

const expectRedirectTo = async (route: GuardedRoute, to: string) => {
  const thrown = await runGuard(route);
  // A TanStack redirect is a Response carrying its navigation options.
  expect(isRedirect(thrown)).toBe(true);
  expect((thrown as { options?: { to?: string } }).options?.to).toBe(to);
};

const expectAdmitted = async (route: GuardedRoute) => {
  expect(await runGuard(route)).toBeUndefined();
};

// The five routes behind the /workspace segment. The parent and three of the
// children gate on the Owner role; /workspace/roles gates on the authorization
// capability instead. This segment exists precisely so that the gate is not
// shared with the personal /settings tree — see the frontend de-tenanting plan.
const ownerGatedRoutes: [string, GuardedRoute][] = [
  ["/workspace", WorkspaceRoute],
  ["/workspace/overview", WorkspaceOverviewRoute],
  ["/workspace/users", WorkspaceUsersRoute],
  ["/workspace/invitations", WorkspaceInvitationsRoute],
];

describe("workspace administration route guards", () => {
  beforeEach(() => {
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
  });

  it.each(ownerGatedRoutes)("admits an Owner to %s", async (_path, route) => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));

    await expectAdmitted(route);
  });

  it.each(ownerGatedRoutes)("redirects a non-Owner away from %s", async (_path, route) => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    await expectRedirectTo(route, "/");
  });

  it.each(ownerGatedRoutes)("redirects an unauthenticated visitor away from %s", async (_path, route) => {
    fetchSession.mockResolvedValue(null);

    await expectRedirectTo(route, "/");
  });

  it("does not let a system admin bypass the Owner requirement", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"], true));

    await expectRedirectTo(WorkspaceRoute, "/");
  });

  it("gates /workspace/roles on the authorization capability rather than ownership", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: true });
    await expectAdmitted(WorkspaceRolesRoute);

    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: false });
    await expectRedirectTo(WorkspaceRolesRoute, "/");
  });
});

describe("the / landing route", () => {
  it("sends a system admin to the control plane", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"], true));

    await expectRedirectTo(IndexRoute, "/admin");
  });

  it("sends everyone else to the dashboard", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    await expectRedirectTo(IndexRoute, "/dashboard");
  });

  it("never renders: it always redirects", async () => {
    fetchSession.mockResolvedValue(null);

    expect(IndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(IndexRoute, "/dashboard");
  });
});
