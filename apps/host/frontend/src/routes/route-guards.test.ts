import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Route as RootRoute } from "./__root";
import { Route as CommunicationsIndexRoute } from "./communications/index";
import { Route as EnergyIndexRoute } from "./energy/index";
import { Route as IndexRoute } from "./index";
import { Route as SettingsIndexRoute } from "./settings/index";
import { Route as WorkspaceRoute } from "./workspace";
import { Route as WorkspaceIndexRoute } from "./workspace/index";
import { Route as WorkspaceInvitationsRoute } from "./workspace/invitations";
import { Route as WorkspaceOverviewRoute } from "./workspace/overview";
import { Route as WorkspaceRolesRoute } from "./workspace/roles";
import { Route as WorkspaceUsersRoute } from "./workspace/users";

const { fetchSession, getAuthorizationMe, fetchBootstrapStatus } = vi.hoisted(() => ({
  fetchSession: vi.fn(),
  getAuthorizationMe: vi.fn(),
  fetchBootstrapStatus: vi.fn(),
}));

vi.mock("../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/auth")>()),
  fetchSession,
}));

vi.mock("../api/account-lifecycle", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/account-lifecycle")>()),
  fetchBootstrapStatus,
}));

vi.mock("../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/authorization")>()),
  getAuthorizationMe,
}));

type GuardedRoute = { options: { beforeLoad?: unknown } };

const sessionWithRoles = (roles: string[], isSystemAdmin = false, mfaEnrollmentRequired = false) => ({
  user: { id: "user-1", displayName: "Test User", email: "test@vantigo.test", roles },
  isSystemAdmin,
  mfaEnrollmentRequired,
});

/** Drives a route's `beforeLoad` guard directly; returns whatever it throws. */
const runGuard = async (route: GuardedRoute, pathname = "/workspace") => {
  const beforeLoad = route.options.beforeLoad as ((context: unknown) => unknown) | undefined;
  if (!beforeLoad) throw new Error("the route declares no beforeLoad guard");
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    await beforeLoad({ context: { queryClient }, location: { pathname } });
  } catch (error) {
    return error;
  }
  return undefined;
};

const expectRedirectTo = async (route: GuardedRoute, to: string, pathname?: string) => {
  const thrown = await runGuard(route, pathname);
  // A TanStack redirect is a Response carrying its navigation options.
  expect(isRedirect(thrown)).toBe(true);
  expect((thrown as { options?: { to?: string } }).options?.to).toBe(to);
};

const expectAdmitted = async (route: GuardedRoute, pathname?: string) => {
  expect(await runGuard(route, pathname)).toBeUndefined();
};

// The root route holds the gates every signed-in page shares: the sign-in
// redirect, the SystemAdmin-only control plane, and the MFA enrolment gate
// that holds an administrator at the security settings until an
// authenticator is enabled.
describe("the root route's MFA enrolment gate", () => {
  beforeEach(() => {
    fetchBootstrapStatus.mockResolvedValue({ available: false });
  });

  it("sends an administrator who must enrol to the security settings from any app page", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"], false, true));

    await expectRedirectTo(RootRoute, "/settings/security", "/dashboard");
    await expectRedirectTo(RootRoute, "/settings/security", "/customers");
  });

  it("holds a SystemAdmin who must enrol too, before the control-plane check runs", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"], true, true));

    await expectRedirectTo(RootRoute, "/settings/security", "/admin");
  });

  it("lets the gated administrator use the settings tree, where enrolment happens", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"], false, true));

    await expectAdmitted(RootRoute, "/settings/security");
    await expectAdmitted(RootRoute, "/settings/profile");
  });

  it("lifts as soon as the session no longer requires enrolment", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"], false, false));

    await expectAdmitted(RootRoute, "/dashboard");
  });

  it("keeps the control plane SystemAdmin-only once the gate is clear", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"], false, false));

    await expectRedirectTo(RootRoute, "/", "/admin");
  });

  it("still sends a visitor without a session to sign in", async () => {
    fetchSession.mockResolvedValue(null);

    await expectRedirectTo(RootRoute, "/sign-in", "/dashboard");
  });
});

// The routes behind the /workspace segment. Three children gate on the Owner
// role; /workspace/roles gates on the authorization capability instead; the
// layout admits whoever passes either, so its sidebar can offer each user the
// pages they may open. This segment exists precisely so that the gate is not
// shared with the personal /settings tree — see the frontend de-tenanting plan.
const ownerGatedRoutes: [string, GuardedRoute][] = [
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
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: false });

    await expectRedirectTo(WorkspaceRoute, "/");
    await expectRedirectTo(WorkspaceUsersRoute, "/");
  });

  it("admits an Owner to the /workspace layout without consulting the authorization capability", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    getAuthorizationMe.mockRejectedValue(new Error("not called"));

    await expectAdmitted(WorkspaceRoute);
  });

  it("admits a non-Owner authorization manager to the /workspace layout, so /workspace/roles is reachable", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: true });

    await expectAdmitted(WorkspaceRoute);
  });

  it("redirects a member without either from the /workspace layout", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: false });

    await expectRedirectTo(WorkspaceRoute, "/");

    fetchSession.mockResolvedValue(null);
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

describe("area index routes", () => {
  it("sends /settings to the profile page", async () => {
    expect(SettingsIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(SettingsIndexRoute, "/settings/profile");
  });

  it("sends /workspace to the overview", async () => {
    expect(WorkspaceIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(WorkspaceIndexRoute, "/workspace/overview");
  });
});

describe("app index routes", () => {
  it("sends /communications to the inbox", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    expect(CommunicationsIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(CommunicationsIndexRoute, "/communications/inbox");
  });

  it("sends /energy to the metering points", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    expect(EnergyIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(EnergyIndexRoute, "/energy/metering-points");
  });
});
