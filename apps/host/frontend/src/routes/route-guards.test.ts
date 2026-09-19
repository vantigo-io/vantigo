import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Route as RootRoute } from "./__root";
import { Route as CommunicationsIndexRoute } from "./communications/index";
import { Route as CustomersIndexRoute } from "./customers/index";
import { Route as EnergyIndexRoute } from "./energy/index";
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
