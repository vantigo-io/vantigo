import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { ModuleAccessGuard } from "./module-access-guard";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
    useRouter: () => ({ history: { back: vi.fn() } }),
    useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
      select({ location: { pathname: "/customers" } }),
  };
});

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

describe("ModuleAccessGuard", () => {
  beforeEach(() => {
    // The guard issues exactly two queries: the session (only to decide whether
    // to ask for permissions) and the authorization payload it filters on.
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: [] }, isPending: false } as never)
        : ({ data: { user: { roles: [] } } } as never),
    );
  });

  it("renders the shared forbidden page when a module destination is denied", () => {
    const client = new QueryClient();
    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <ModuleAccessGuard>Allowed content</ModuleAccessGuard>
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText("Allowed content")).not.toBeInTheDocument();
  });
});
