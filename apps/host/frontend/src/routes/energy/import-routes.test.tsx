import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ImportRoutePage } from "./-import-pages";
import { Route as ImportRoute } from "./metering-points/import.$importId";

// The wrapper's job is the three capabilities (energy import design D5, D9);
// the page itself is the package's, replaced here by a probe that prints what
// it was handed.
vi.mock("@vantigo/energy-ui/pages/metering-points.import.$importId", () => ({
  ImportPage: ({
    importId,
    capabilities,
  }: {
    importId: number;
    capabilities: { canCreate: boolean; canUpdate: boolean; canLinkCustomers: boolean };
  }) => (
    <div>
      <span data-testid="import-id">{importId}</span>
      <span data-testid="can-create">{String(capabilities.canCreate)}</span>
      <span data-testid="can-update">{String(capabilities.canUpdate)}</span>
      <span data-testid="can-link-customers">{String(capabilities.canLinkCustomers)}</span>
    </div>
  ),
}));
vi.mock("@vantigo/energy-ui/pages/metering-points.import.index", () => ({ ImportStartPage: () => <div>start</div> }));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useParams: () => ({ importId: 7 }),
}));

const { fetchSession, getAuthorizationMe } = vi.hoisted(() => ({ fetchSession: vi.fn(), getAuthorizationMe: vi.fn() }));
vi.mock("../../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/auth")>()),
  fetchSession,
}));
vi.mock("../../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/authorization")>()),
  getAuthorizationMe,
}));

const withPermissions = (permissions: string[]) => {
  fetchSession.mockResolvedValue({ user: { id: "u1", displayName: "Test", email: "t@example.test", roles: [] } });
  getAuthorizationMe.mockResolvedValue({ permissions });
};

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ImportRoutePage />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("the metering-point import route", () => {
  it("parses the import id as a number and prints it back", () => {
    const params = ImportRoute.options.params as {
      parse: (raw: { importId: string }) => { importId: number };
      stringify: (parsed: { importId: number }) => { importId: string };
    };
    expect(params.parse({ importId: "7" })).toEqual({ importId: 7 });
    expect(params.stringify({ importId: 7 })).toEqual({ importId: "7" });
  });

  it("creates only with both the metering-point and the meter permission", async () => {
    withPermissions(["energy:import", "energy:metering-points-view", "energy:metering-points-manage"]);
    renderPage();
    await waitFor(() => expect(screen.getByTestId("can-update")).toHaveTextContent("true"));
    expect(screen.getByTestId("import-id")).toHaveTextContent("7");
    expect(screen.getByTestId("can-create")).toHaveTextContent("false");
    expect(screen.getByTestId("can-link-customers")).toHaveTextContent("false");
  });

  it("links customers with the supply-period permission", async () => {
    withPermissions([
      "energy:import",
      "energy:metering-points-manage",
      "energy:meters-manage",
      "energy:supply-periods-manage",
    ]);
    renderPage();
    await waitFor(() => expect(screen.getByTestId("can-create")).toHaveTextContent("true"));
    expect(screen.getByTestId("can-link-customers")).toHaveTextContent("true");
  });
});
