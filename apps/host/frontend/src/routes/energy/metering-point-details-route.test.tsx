import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MeteringPointDetailsRoutePage } from "./-metering-point-details";

// Pins the production call site: the metering point route passes
// canImportReadings — energy:import, energy:consumption-manage and
// energy:consumption-view together — through to the package's page, which puts
// Import readings in the Consumption card (energy import design D8).
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/energy-ui", () => ({
  MeteringPointDetailsPage: ({
    canImportReadings,
    canViewTariffs,
  }: {
    canImportReadings?: boolean;
    canViewTariffs?: boolean;
  }) => (
    <>
      <span data-testid="can-import-readings">{String(Boolean(canImportReadings))}</span>
      <span data-testid="can-view-tariffs">{String(Boolean(canViewTariffs))}</span>
    </>
  ),
}));

const renderPage = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <MeteringPointDetailsRoutePage />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const withPermissions = (permissions: string[]) =>
  vi
    .mocked(useQuery)
    .mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );

afterEach(cleanup);

describe("the metering point route", () => {
  it("passes canImportReadings only with energy:import and both consumption keys", () => {
    withPermissions(["energy:import", "energy:consumption-manage", "energy:consumption-view"]);
    renderPage();
    expect(screen.getByTestId("can-import-readings")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:consumption-manage", "energy:consumption-view"]);
    renderPage();
    expect(screen.getByTestId("can-import-readings")).toHaveTextContent("false");
    cleanup();

    withPermissions(["energy:import", "energy:consumption-view"]);
    renderPage();
    expect(screen.getByTestId("can-import-readings")).toHaveTextContent("false");
  });

  // The edit form and the supply-period dialogs list tariffs only with energy:tariffs-view.
  it("passes canViewTariffs only with energy:tariffs-view", () => {
    withPermissions(["energy:supply-periods-manage", "energy:tariffs-view"]);
    renderPage();
    expect(screen.getByTestId("can-view-tariffs")).toHaveTextContent("true");
    cleanup();

    withPermissions(["energy:supply-periods-manage", "energy:tariffs-manage"]);
    renderPage();
    expect(screen.getByTestId("can-view-tariffs")).toHaveTextContent("false");
  });
});
