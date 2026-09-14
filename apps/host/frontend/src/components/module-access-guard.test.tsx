import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
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
