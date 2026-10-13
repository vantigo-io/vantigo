import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ImportRoutePage } from "./-import-pages";
import { Route as ImportRoute } from "./import.$importId";

// The wrapper's job is the four capabilities (customers import wizard design
// D4, D8); the page itself is the package's, replaced here by a probe that
// prints what it was handed.
vi.mock("@vantigo/customers-ui/pages/import.$importId", () => ({
  ImportPage: ({
    importId,
    capabilities,
  }: {
    importId: number;
    capabilities: { canCreate: boolean; canUpdate: boolean; canMapIdentity: boolean; canMapBilling: boolean };
  }) => (
    <div>
      <span data-testid="import-id">{importId}</span>
      <span data-testid="can-create">{String(capabilities.canCreate)}</span>
      <span data-testid="can-update">{String(capabilities.canUpdate)}</span>
      <span data-testid="can-map-identity">{String(capabilities.canMapIdentity)}</span>
      <span data-testid="can-map-billing">{String(capabilities.canMapBilling)}</span>
    </div>
  ),
}));
vi.mock("@vantigo/customers-ui/pages/import.index", () => ({ ImportStartPage: () => <div>start</div> }));
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

describe("the import route", () => {
  it("parses the import id as a number and prints it back", () => {
    const params = ImportRoute.options.params as {
      parse: (raw: { importId: string }) => { importId: number };
      stringify: (parsed: { importId: number }) => { importId: string };
    };
    expect(params.parse({ importId: "7" })).toEqual({ importId: 7 });
    expect(params.stringify({ importId: 7 })).toEqual({ importId: "7" });
  });

  it("hands the page the four capabilities, each from its own key", async () => {
    withPermissions(["customers:import", "customers:view", "customers:create", "customers:billing-manage"]);
    renderPage();
    await waitFor(() => expect(screen.getByTestId("can-create")).toHaveTextContent("true"));
    expect(screen.getByTestId("import-id")).toHaveTextContent("7");
    expect(screen.getByTestId("can-update")).toHaveTextContent("false");
    expect(screen.getByTestId("can-map-identity")).toHaveTextContent("false");
    expect(screen.getByTestId("can-map-billing")).toHaveTextContent("true");
  });
});
