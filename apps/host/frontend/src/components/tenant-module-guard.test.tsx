import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { TenantModuleGuard } from "./tenant-module-guard";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
    useRouter: () => ({ history: { back: vi.fn() } }),
    useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
      select({ location: { pathname: "/acme/customers" } }),
  };
});

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

describe("TenantModuleGuard", () => {
  beforeEach(() => {
    vi.mocked(useQuery).mockImplementation((options) => {
      const key = options.queryKey[0];
      if (key === "auth") return { data: { activeTenantId: "tenant-1" } } as never;
      if (key === "tenant-capabilities") return { data: { modules: [] }, isPending: false } as never;
      return { data: { permissions: [] }, isPending: false } as never;
    });
  });

  it("renders the shared forbidden page when a module is denied", () => {
    const client = new QueryClient();
    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <TenantModuleGuard tenantSlug="acme">Allowed content</TenantModuleGuard>
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText("Allowed content")).not.toBeInTheDocument();
  });
});
