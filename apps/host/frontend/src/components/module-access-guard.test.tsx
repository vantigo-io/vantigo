import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { ModuleAccessGuard } from "./module-access-guard";

// Mutable so each test can steer useRouterState's pathname; vi.hoisted keeps
// it reachable from inside the hoisted vi.mock factory below.
const routerState = vi.hoisted(() => ({ pathname: "/customers" }));

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
    useRouter: () => ({ history: { back: vi.fn() } }),
    useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
      select({ location: { pathname: routerState.pathname } }),
  };
});

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

const renderGuard = () => {
  const client = new QueryClient();
  render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <ModuleAccessGuard>Allowed content</ModuleAccessGuard>
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("ModuleAccessGuard", () => {
  beforeEach(() => {
    routerState.pathname = "/customers";
    // The guard issues exactly two queries: the session (only to decide whether
    // to ask for permissions) and the authorization payload it filters on.
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: [] }, isPending: false } as never)
        : ({ data: { user: { roles: [] } } } as never),
    );
  });

  it("renders the shared forbidden page when a module destination is denied", () => {
    renderGuard();

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText("Allowed content")).not.toBeInTheDocument();
  });

  it("renders the protected content when the user holds the destination's required permission", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:view"] }, isPending: false } as never)
        : ({ data: { user: { roles: [] } } } as never),
    );

    renderGuard();

    expect(screen.getByText("Allowed content")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
  });

  // The rules are derived from the app registry, so a new app is guarded the
  // moment it registers a sidebar entry. Projects is pinned here because its
  // whole app sits behind one permission: losing the rule would open every
  // project page to a caller the backend still refuses.
  it.each(["/projects", "/projects/42", "/projects/42/billing"])("guards %s behind projects:access", (pathname) => {
    routerState.pathname = pathname;

    renderGuard();

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText("Allowed content")).not.toBeInTheDocument();
  });

  it("renders the projects pages for a caller holding projects:access", () => {
    routerState.pathname = "/projects/42";
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["projects:access"] }, isPending: false } as never)
        : ({ data: { user: { roles: [] } } } as never),
    );

    renderGuard();

    expect(screen.getByText("Allowed content")).toBeInTheDocument();
  });

  const renderGuardFor = (pathname: string, permissions: string[]) => {
    routerState.pathname = pathname;
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions }, isPending: false } as never)
        : ({ data: { user: { roles: [] } } } as never),
    );
    renderGuard();
  };

  // Time's pages sit behind four different permissions, so the rules derived
  // from the registry differ per prefix rather than covering the whole app.
  it.each([
    ["/time", ["time:access"]],
    ["/time/day", ["time:access"]],
    ["/time/people", ["time:view-all"]],
    ["/time/settings", ["time:manage"]],
  ])("guards %s behind %s", (pathname, permissions) => {
    renderGuardFor(pathname, []);
    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();

    cleanup();
    renderGuardFor(pathname, permissions);
    expect(screen.getByText("Allowed content")).toBeInTheDocument();
  });

  // The approval queue is the one page whose sidebar entry and whose guard
  // disagree on purpose: the entry is offered to `time:approve` holders, but a
  // project manager approves through their role and reaches the queue from the
  // dashboard's attention list or by pasting the URL. The backend answers 403
  // to a caller who approves nothing, so the guard only has to let the app's
  // own permission through.
  it("lets a time:access holder open the approval queue, which their role may fill", () => {
    renderGuardFor("/time/approvals", ["time:access"]);

    expect(screen.getByText("Allowed content")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
  });

  it("still refuses the approval queue to a caller outside the Time app", () => {
    renderGuardFor("/time/approvals", ["projects:access"]);

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
  });

  // Expenses' pages sit behind three different permissions, the same shape as
  // Time's.
  it.each([
    ["/expenses", ["expenses:access"]],
    ["/expenses/reimbursements", ["expenses:manage"]],
    ["/expenses/settings", ["expenses:manage"]],
  ])("guards %s behind %s", (pathname, permissions) => {
    renderGuardFor(pathname, []);
    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();

    cleanup();
    renderGuardFor(pathname, permissions);
    expect(screen.getByText("Allowed content")).toBeInTheDocument();
  });

  // Expenses' approval queue is Time's own seam again: the sidebar entry is
  // for expenses:approve holders, but a project manager approves their own
  // project's expenses through their role alone and reaches the queue from the
  // dashboard's attention list or by pasting the URL. The backend answers 403
  // to a caller who approves nothing, so the guard only asks for the app.
  it("lets an expenses:access holder open the approval queue, which their role may fill", () => {
    renderGuardFor("/expenses/approvals", ["expenses:access"]);

    expect(screen.getByText("Allowed content")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
  });

  it("still refuses the expenses approval queue to a caller outside the Expenses app", () => {
    renderGuardFor("/expenses/approvals", ["projects:access"]);

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
  });

  // The `!rule` fall-through is what keeps the guard's now-global root mount
  // (task 4 of the frontend de-tenanting plan) off destinations that carry no
  // `module` in the navigation catalog. Losing this silently would gate
  // personal settings and workspace/system administration behind module
  // permissions nobody holds.
  it.each(["/settings", "/workspace", "/admin", "/workspace/overview"])(
    "renders children unguarded on %s, which matches no module destination",
    (pathname) => {
      routerState.pathname = pathname;

      renderGuard();

      expect(screen.getByText("Allowed content")).toBeInTheDocument();
      expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
    },
  );
});
