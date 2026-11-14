import { QueryClient } from "@tanstack/react-query";
import { isNotFound, isRedirect } from "@tanstack/react-router";
import { connectionQueryOptions, type EntraConnection } from "@vantigo/entra-ui";
import { type GoogleConnection, connectionQueryOptions as googleConnectionQueryOptions } from "@vantigo/google-ui";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Route as RootRoute } from "./__root";
import { Route as CommunicationsIndexRoute } from "./communications/index";
import { Route as CustomersIndexRoute } from "./customers/index";
import { Route as EnergyIndexRoute } from "./energy/index";
import { Route as EntraRoute } from "./entra";
import { Route as EntraIndexRoute } from "./entra/index";
import { Route as IndexRoute } from "./index";
import { Route as ProductsIndexRoute } from "./products/index";
import { Route as ProjectTasksRoute } from "./projects/$projectId.tasks";
import { Route as ProjectsIndexRoute } from "./projects/index";
import { Route as MyTasksRoute } from "./projects/my-tasks";
import { Route as SettingsIndexRoute } from "./settings/index";
import { Route as TimeApprovalsRoute } from "./time/approvals";
import { Route as TimeDayRoute } from "./time/day";
import { Route as TimeIndexRoute } from "./time/index";
import { Route as TimePeopleRoute } from "./time/people";
import { Route as TimeSettingsRoute } from "./time/settings";
import { Route as WorkspaceRoute } from "./workspace";
import { Route as WorkspaceAiRoute } from "./workspace/ai";
import { Route as WorkspaceBankRoute } from "./workspace/bank";
import { Route as WorkspaceEntraRoute } from "./workspace/entra";
import { Route as WorkspaceEntraAccountsRoute } from "./workspace/entra/accounts";
import { Route as WorkspaceEntraConnectionRoute } from "./workspace/entra/connection";
import { Route as WorkspaceEntraIndexRoute } from "./workspace/entra/index";
import { Route as WorkspaceEntraOutlookRoute } from "./workspace/entra/outlook";
import { Route as WorkspaceGoogleRoute } from "./workspace/google";
import { Route as WorkspaceGoogleAccountsRoute } from "./workspace/google/accounts";
import { Route as WorkspaceGoogleCalendarGmailRoute } from "./workspace/google/calendar-gmail";
import { Route as WorkspaceGoogleConnectionRoute } from "./workspace/google/connection";
import { Route as WorkspaceGoogleIndexRoute } from "./workspace/google/index";
import { Route as WorkspaceIndexRoute } from "./workspace/index";
import { Route as WorkspaceInvitationsRoute } from "./workspace/invitations";
import { Route as WorkspaceMetaRoute } from "./workspace/meta";
import { Route as WorkspaceOverviewRoute } from "./workspace/overview";
import { Route as WorkspaceRolesRoute } from "./workspace/roles";
import { Route as WorkspaceShippingRoute } from "./workspace/shipping";
import { Route as WorkspaceUsersRoute } from "./workspace/users";

const { fetchSession, getAuthorizationMe, fetchBootstrapStatus, fetchAiSettings } = vi.hoisted(() => ({
  fetchSession: vi.fn(),
  getAuthorizationMe: vi.fn(),
  fetchBootstrapStatus: vi.fn(),
  fetchAiSettings: vi.fn(),
}));

vi.mock("../api/ai", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/ai")>()),
  fetchAiSettings,
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
    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: false });

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
    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: false });

    await expectRedirectTo(WorkspaceRoute, "/");

    fetchSession.mockResolvedValue(null);
    await expectRedirectTo(WorkspaceRoute, "/");
  });

  it("keeps the roles page's view in the URL and falls back to the roles view for anything else", () => {
    const validate = WorkspaceRolesRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;
    expect(validate({ section: "delegations" })).toEqual({ section: "delegations" });
    expect(validate({ section: "assignments" })).toEqual({ section: "assignments" });
    expect(validate({ section: "bogus" })).toEqual({ section: undefined });
    expect(validate({})).toEqual({ section: undefined });
  });

  it("gates /workspace/roles on the authorization capability rather than ownership", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: true });
    await expectAdmitted(WorkspaceRolesRoute);

    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: false });
    await expectRedirectTo(WorkspaceRolesRoute, "/");
  });
});

// Integrations › AI models (Integrations design T13): the Owner's, and there only while the
// settings endpoint answers; a 404 from it is the instance having no AI provider.
describe("the AI models route", () => {
  const apiError = (status: number) => Object.assign(new Error(`status ${status}`), { status });

  beforeEach(() => {
    fetchAiSettings.mockReset();
    fetchAiSettings.mockResolvedValue({ provider: "openai", allowedModels: ["m"], modules: [] });
  });

  it("admits an Owner while the instance has an AI provider", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    await expectAdmitted(WorkspaceAiRoute, "/workspace/ai");
    expect(fetchAiSettings).toHaveBeenCalledOnce();
  });

  it("answers not-found, as an unknown URL does, when the instance has no AI provider", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    fetchAiSettings.mockRejectedValue(apiError(404));

    const thrown = await runGuard(WorkspaceAiRoute, "/workspace/ai");
    expect(isNotFound(thrown)).toBe(true);
  });

  it("lets the page render and say it could not load when the settings fail for another reason", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    fetchAiSettings.mockRejectedValue(apiError(500));

    await expectAdmitted(WorkspaceAiRoute, "/workspace/ai");
  });

  it("sends anyone but an Owner home without asking the API", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    await expectRedirectTo(WorkspaceAiRoute, "/", "/workspace/ai");

    fetchSession.mockResolvedValue(null);
    await expectRedirectTo(WorkspaceAiRoute, "/", "/workspace/ai");
    expect(fetchAiSettings).not.toHaveBeenCalled();
  });
});

// Integrations › Microsoft Entra ID (Integrations design T2, T3): the workspace
// layout admits a holder of entra:manage while the module is on, and the page's
// own route asks the module and the permission again.
describe("the Integrations routes", () => {
  const injectModules = (modules: string[]) => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  };
  const withEntra = ["staff", "entra"];
  const withoutEntra = ["staff"];

  beforeEach(() => injectModules(withEntra));
  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  const entraManager = { permissions: ["entra:manage"], canManageAuthorization: false };

  it("admits an Entra manager who is not an Owner to the /workspace layout while the module is on", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(entraManager);

    await expectAdmitted(WorkspaceRoute);
  });

  it("sends that same person home when the Entra module is off, and a member without the permission always", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(entraManager);
    injectModules(withoutEntra);
    await expectRedirectTo(WorkspaceRoute, "/");

    injectModules(withEntra);
    getAuthorizationMe.mockResolvedValue({ permissions: ["staff:access"], canManageAuthorization: false });
    await expectRedirectTo(WorkspaceRoute, "/");
  });

  it("admits an Owner and an Entra manager to /workspace/entra, and an authorization manager without entra:manage not", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
    await expectAdmitted(WorkspaceEntraRoute);

    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(entraManager);
    await expectAdmitted(WorkspaceEntraRoute);

    getAuthorizationMe.mockResolvedValue({ permissions: [], canManageAuthorization: true });
    await expectRedirectTo(WorkspaceEntraRoute, "/");
  });

  it("answers the not-enabled page in place for /workspace/entra while the Entra module is off", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
    injectModules(withoutEntra);

    const thrown = await runGuard(WorkspaceEntraRoute, "/workspace/entra");
    expect(isNotFound(thrown)).toBe(true);
    expect((thrown as { data?: unknown }).data).toEqual({ moduleDisabled: "entra" });
  });

  it("keeps the four tabs plain children of that gate", () => {
    for (const route of [
      WorkspaceEntraIndexRoute,
      WorkspaceEntraConnectionRoute,
      WorkspaceEntraAccountsRoute,
      WorkspaceEntraOutlookRoute,
    ]) {
      expect(route.options.component).toBeDefined();
    }
    for (const route of [WorkspaceEntraConnectionRoute, WorkspaceEntraAccountsRoute, WorkspaceEntraOutlookRoute]) {
      expect(route.options.beforeLoad).toBeUndefined();
    }
  });

  describe("the Status tab as the way in", () => {
    // What stepsDone reads of a connection: registered or not.
    const connection = (registered: boolean) =>
      ({
        tenantId: registered ? "11111111-1111-1111-1111-111111111111" : undefined,
        clientId: registered ? "22222222-2222-2222-2222-222222222222" : undefined,
        credentials: [],
        status: "unconfigured",
        permissionsMissing: [],
        settings: {},
      }) as unknown as EntraConnection;

    const runIndexGuard = async (seed: EntraConnection | undefined) => {
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      if (seed) queryClient.setQueryData(connectionQueryOptions().queryKey, seed);
      try {
        await (WorkspaceEntraIndexRoute.options.beforeLoad as (context: unknown) => unknown)({
          context: { queryClient },
        });
      } catch (error) {
        return error;
      }
      return undefined;
    };

    it("opens on Connection while step 1 is not done", async () => {
      const thrown = await runIndexGuard(connection(false));
      expect(isRedirect(thrown)).toBe(true);
      expect((thrown as { options?: { to?: string } }).options?.to).toBe("/workspace/entra/connection");
    });

    it("opens on Status once step 1 is done", async () => {
      expect(await runIndexGuard(connection(true))).toBeUndefined();
    });

    it("opens on Status, which says so, when the connection cannot be read", async () => {
      vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
      try {
        expect(await runIndexGuard(undefined)).toBeUndefined();
      } finally {
        vi.unstubAllGlobals();
      }
    });
  });

  it("sends /entra and /entra/ on to /workspace/entra, for good", async () => {
    await expectRedirectTo(EntraRoute, "/workspace/entra");
    await expectRedirectTo(EntraIndexRoute, "/workspace/entra");
    for (const route of [EntraRoute, EntraIndexRoute]) {
      expect(route.options.component).toBeUndefined();
      const thrown = await runGuard(route, "/entra");
      expect((thrown as { options?: { replace?: boolean } }).options?.replace).toBe(true);
    }
  });
});

// Integrations › Google Workspace (Google Workspace design G16): the workspace
// layout admits a holder of google:manage while the module is on, and the
// page's own route asks the module and the permission again, as Entra's does.
describe("the Google Workspace routes", () => {
  const injectModules = (modules: string[]) => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  };
  const withGoogle = ["staff", "google"];
  const withoutGoogle = ["staff", "entra"];

  beforeEach(() => injectModules(withGoogle));
  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  const googleManager = { permissions: ["google:manage"], canManageAuthorization: false };

  it("admits a Google manager who is not an Owner to the /workspace layout while the module is on", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(googleManager);

    await expectAdmitted(WorkspaceRoute);
  });

  it("sends that same person home when the Google module is off, and a member without the permission always", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(googleManager);
    injectModules(withoutGoogle);
    await expectRedirectTo(WorkspaceRoute, "/");

    injectModules(withGoogle);
    getAuthorizationMe.mockResolvedValue({ permissions: ["entra:manage"], canManageAuthorization: false });
    await expectRedirectTo(WorkspaceRoute, "/");
  });

  it("admits an Owner and a Google manager to /workspace/google, and an authorization manager without google:manage not", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
    await expectAdmitted(WorkspaceGoogleRoute);

    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(googleManager);
    await expectAdmitted(WorkspaceGoogleRoute);

    getAuthorizationMe.mockResolvedValue({ permissions: ["entra:manage"], canManageAuthorization: true });
    await expectRedirectTo(WorkspaceGoogleRoute, "/");
  });

  it("answers the not-enabled page in place for /workspace/google while the Google module is off", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
    injectModules(withoutGoogle);

    const thrown = await runGuard(WorkspaceGoogleRoute, "/workspace/google");
    expect(isNotFound(thrown)).toBe(true);
    expect((thrown as { data?: unknown }).data).toEqual({ moduleDisabled: "google" });
  });

  it("keeps the four tabs plain children of that gate", () => {
    for (const route of [
      WorkspaceGoogleIndexRoute,
      WorkspaceGoogleConnectionRoute,
      WorkspaceGoogleAccountsRoute,
      WorkspaceGoogleCalendarGmailRoute,
    ]) {
      expect(route.options.component).toBeDefined();
    }
    for (const route of [
      WorkspaceGoogleConnectionRoute,
      WorkspaceGoogleAccountsRoute,
      WorkspaceGoogleCalendarGmailRoute,
    ]) {
      expect(route.options.beforeLoad).toBeUndefined();
    }
  });

  describe("the Status tab as the way in", () => {
    // What opensOnConnection reads of a connection: its status and its keys.
    const connection = (status: GoogleConnection["status"], keys: number) =>
      ({
        status,
        credentials: Array.from({ length: keys }, (_, index) => ({
          id: index + 1,
          kind: "service_account_key",
          createdAt: "2026-11-01T08:00:00Z",
          inUse: true,
        })),
      }) as unknown as GoogleConnection;

    const runIndexGuard = async (seed: GoogleConnection | undefined) => {
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      if (seed) queryClient.setQueryData(googleConnectionQueryOptions().queryKey, seed);
      try {
        await (WorkspaceGoogleIndexRoute.options.beforeLoad as (context: unknown) => unknown)({
          context: { queryClient },
        });
      } catch (error) {
        return error;
      }
      return undefined;
    };

    it("opens on Connection while unconfigured with no key", async () => {
      const thrown = await runIndexGuard(connection("unconfigured", 0));
      expect(isRedirect(thrown)).toBe(true);
      expect((thrown as { options?: { to?: string } }).options?.to).toBe("/workspace/google/connection");
    });

    it("opens on Status once a key is uploaded, or once the connection is anything but unconfigured", async () => {
      expect(await runIndexGuard(connection("unconfigured", 1))).toBeUndefined();
      expect(await runIndexGuard(connection("failing", 0))).toBeUndefined();
    });

    it("opens on Status, which says so, when the connection cannot be read", async () => {
      vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
      try {
        expect(await runIndexGuard(undefined)).toBeUndefined();
      } finally {
        vi.unstubAllGlobals();
      }
    });
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

// The customers and products lists take `create` in the URL so Spotlight's quick
// actions can land on the list with the create form already open. It is only
// ever present when true, so an ordinary list URL stays as short as before.
// My tasks takes it the same way, for the Create task action, which opens a
// project picker there because nothing in the spotlight knows the project.
describe("list routes' create intent", () => {
  it.each([
    ["customers", CustomersIndexRoute],
    ["products", ProductsIndexRoute],
    ["projects", ProjectsIndexRoute],
    ["my tasks", MyTasksRoute],
  ])("keeps create in the %s list URL only when it is true", (_name, route) => {
    const validate = route.options.validateSearch as (search: Record<string, unknown>) => { create?: boolean };
    expect(validate({ create: true }).create).toBe(true);
    expect(validate({ create: "true" }).create).toBe(true);
    expect(validate({ create: false })).not.toHaveProperty("create", true);
    expect(validate({ create: "yes" })).not.toHaveProperty("create", true);
    expect(validate({})).not.toHaveProperty("create", true);
  });
});

// The projects list keeps every filter in the URL, so a pasted link restores
// the same list. The defaults are the ones navSearchFor("projects-list") hands
// Spotlight, and an unknown status falls back to "any" rather than reaching the
// API as a value it would reject.
describe("the projects list's search params", () => {
  const validate = ProjectsIndexRoute.options.validateSearch as (
    search: Record<string, unknown>,
  ) => Record<string, unknown>;

  it("falls back to the first page, no search, any status and everyone's projects", () => {
    expect(validate({})).toEqual({
      page: 1,
      search: "",
      status: "",
      customerId: undefined,
      internal: undefined,
      mine: false,
    });
  });

  it("keeps the filters a pasted link carries and drops a status it does not know", () => {
    expect(
      validate({ page: "3", search: "roof", status: "on-hold", customerId: "7", internal: "true", mine: "true" }),
    ).toEqual({ page: 3, search: "roof", status: "on-hold", customerId: 7, internal: true, mine: true });
    expect(validate({ status: "archived" }).status).toBe("");
    expect(validate({ page: "0" }).page).toBe(1);
  });

  // A hand-edited URL must not reach the API as `customerId=NaN` or narrow the
  // list to customer projects because `internal` held a word nobody meant as
  // false. Both fall back to "no filter", which is what an absent one does.
  it("drops a customer filter that is not a positive integer", () => {
    expect(validate({ customerId: "abc" }).customerId).toBeUndefined();
    expect(validate({ customerId: "1.5" }).customerId).toBeUndefined();
    expect(validate({ customerId: "0" }).customerId).toBeUndefined();
    expect(validate({ customerId: "-2" }).customerId).toBeUndefined();
    expect(validate({ customerId: "" }).customerId).toBeUndefined();
    expect(validate({ customerId: "7" }).customerId).toBe(7);
  });

  it("takes the internal filter only from the two literal booleans", () => {
    expect(validate({ internal: "true" }).internal).toBe(true);
    expect(validate({ internal: "false" }).internal).toBe(false);
    expect(validate({ internal: "maybe" }).internal).toBeUndefined();
    expect(validate({ internal: "1" }).internal).toBeUndefined();
  });
});

// The Tasks tab deep-links a single task: /projects/31/tasks?task=1042, the
// URL the package's taskUrl builds and My tasks links to. The route opens the
// drawer on it and drops it again when the drawer closes, so a hand-edited or
// stale value must degrade to "no task" rather than reach the API as NaN.
describe("the project tasks tab's search params", () => {
  const validate = ProjectTasksRoute.options.validateSearch as (search: Record<string, unknown>) => {
    task?: number | undefined;
  };

  it("keeps the task a deep link names, as a number", () => {
    expect(validate({ task: "1042" }).task).toBe(1042);
    expect(validate({ task: 1042 }).task).toBe(1042);
  });

  it("drops anything that is not a positive integer, the ordinary tab URL included", () => {
    for (const task of ["abc", "1.5", "0", "-2", "", true, null]) {
      expect(validate({ task }).task).toBeUndefined();
    }
    expect(validate({}).task).toBeUndefined();
  });
});

// The Time app's pages read their whole state from the URL, so a pasted or
// hand-edited link must degrade to the page's own default rather than reach
// the API as `weekStart=NaN` or a week that is not a Monday. The package
// normalises a date to its Monday itself; the host's job is only to refuse
// anything that is not a calendar date.
describe("the time routes' search params", () => {
  const validatorFor = (route: { options: { validateSearch?: unknown } }) =>
    route.options.validateSearch as (search: Record<string, unknown>) => Record<string, unknown>;

  it.each([
    ["my week", TimeIndexRoute, "week"],
    ["the day view", TimeDayRoute, "date"],
  ])("keeps a calendar date in %s's URL and drops anything else", (_name, route, key) => {
    const validate = validatorFor(route);
    expect(validate({ [key]: "2026-09-14" })).toEqual({ [key]: "2026-09-14" });
    // A Sunday, a Thursday: the page picks the week, so any real date is kept.
    expect(validate({ [key]: "2026-09-20" })).toEqual({ [key]: "2026-09-20" });
    for (const value of ["2026-02-30", "2026-9-14", "not-a-date", "", 20260914, null, true]) {
      expect(validate({ [key]: value })).toEqual({ [key]: undefined });
    }
    expect(validate({})).toEqual({ [key]: undefined });
  });

  it("keeps the approval queue's page as a positive whole number", () => {
    const validate = validatorFor(TimeApprovalsRoute);
    expect(validate({ page: "3" })).toEqual({ page: 3 });
    expect(validate({ page: 3 })).toEqual({ page: 3 });
    for (const page of ["0", "-1", "1.5", "abc", ""]) expect(validate({ page })).toEqual({ page: undefined });
    expect(validate({})).toEqual({ page: undefined });
  });

  // The API refuses a window outside 1–12, so a link carrying 50 has to arrive
  // as "no window at all" and let the page fall back to its default of four.
  it("keeps the people overview's window inside the one to twelve weeks the API takes", () => {
    const validate = validatorFor(TimePeopleRoute);
    expect(validate({ weeks: "8" })).toEqual({ weeks: 8 });
    expect(validate({ weeks: 1 })).toEqual({ weeks: 1 });
    expect(validate({ weeks: 12 })).toEqual({ weeks: 12 });
    for (const weeks of ["0", "13", "50", "-4", "2.5", "many", ""]) {
      expect(validate({ weeks })).toEqual({ weeks: undefined });
    }
    expect(validate({})).toEqual({ weeks: undefined });
  });

  it("gives the settings page no search params of its own", () => {
    expect(TimeSettingsRoute.options.validateSearch).toBeUndefined();
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

// Integrations › Shipping (orders design O51): the workspace layout admits a
// holder of orders:carriers-manage while the Orders module is on, and the
// page's own route asks the module and the permission again.
describe("the Shipping route", () => {
  const injectModules = (modules: string[]) => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  };
  const withOrders = ["customers", "products", "invoices", "orders"];
  const withoutOrders = ["customers", "products", "invoices"];

  beforeEach(() => injectModules(withOrders));
  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  const carrierManager = { permissions: ["orders:access", "orders:carriers-manage"], canManageAuthorization: false };

  it("admits a carrier manager who is not an Owner to /workspace and /workspace/shipping while Orders is on", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(carrierManager);
    await expectAdmitted(WorkspaceRoute);
    await expectAdmitted(WorkspaceShippingRoute);
  });

  it("sends a member without orders:carriers-manage home, and answers the not-enabled page while Orders is off", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["orders:access"], canManageAuthorization: false });
    await expectRedirectTo(WorkspaceRoute, "/");
    await expectRedirectTo(WorkspaceShippingRoute, "/");

    getAuthorizationMe.mockResolvedValue(carrierManager);
    injectModules(withoutOrders);
    await expectRedirectTo(WorkspaceRoute, "/");
    const thrown = await runGuard(WorkspaceShippingRoute, "/workspace/shipping");
    expect(isNotFound(thrown)).toBe(true);
    expect((thrown as { data?: unknown }).data).toEqual({ moduleDisabled: "orders" });
  });
});

// Integrations › Bank (invoices bank feed design F9): the workspace layout
// admits a holder of invoices:manage while the Invoices module is on, and the
// page's own route asks the module and the permission again.
describe("the Bank route", () => {
  const injectModules = (modules: string[]) => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  };
  const withInvoices = ["customers", "products", "invoices"];
  const withoutInvoices = ["customers", "products"];

  beforeEach(() => injectModules(withInvoices));
  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  const invoiceManager = { permissions: ["invoices:access", "invoices:manage"], canManageAuthorization: false };

  it("admits an invoice manager who is not an Owner to /workspace and /workspace/bank while Invoices is on", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue(invoiceManager);
    await expectAdmitted(WorkspaceRoute);
    await expectAdmitted(WorkspaceBankRoute);
  });

  it("sends a member without invoices:manage home, and answers the not-enabled page while Invoices is off", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({
      permissions: ["invoices:access", "invoices:payments"],
      canManageAuthorization: false,
    });
    await expectRedirectTo(WorkspaceRoute, "/");
    await expectRedirectTo(WorkspaceBankRoute, "/");

    getAuthorizationMe.mockResolvedValue(invoiceManager);
    injectModules(withoutInvoices);
    await expectRedirectTo(WorkspaceRoute, "/");
    const thrown = await runGuard(WorkspaceBankRoute, "/workspace/bank");
    expect(isNotFound(thrown)).toBe(true);
    expect((thrown as { data?: unknown }).data).toEqual({ moduleDisabled: "invoices" });
  });
});

// Integrations › Meta (Messenger design N9): the Owner's, while the Meta module is on. The module has no
// permission, so a holder of every permission who is not an Owner is sent home.
describe("the Meta route", () => {
  const injectModules = (modules: string[]) => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  };

  beforeEach(() => injectModules(["customers", "communications", "meta"]));
  afterEach(() => {
    delete window.__VANTIGO_APP__;
  });

  it("admits an Owner while the module is on", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    await expectAdmitted(WorkspaceMetaRoute);
  });

  it("sends a member home, whatever the member holds", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"], canManageAuthorization: true });
    await expectRedirectTo(WorkspaceMetaRoute, "/");
  });

  it("answers the not-enabled page to an Owner while the module is off", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Owner"]));
    injectModules(["customers", "communications"]);
    const thrown = await runGuard(WorkspaceMetaRoute, "/workspace/meta");
    expect(isNotFound(thrown)).toBe(true);
    expect((thrown as { data?: unknown }).data).toEqual({ moduleDisabled: "meta" });
  });
});
