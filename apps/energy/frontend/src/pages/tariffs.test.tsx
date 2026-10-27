import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { TariffSummary } from "../api/tariffs";
import { stubFetch } from "../test/fetch";
import { TariffsPage } from "./tariffs";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

const json = (body: unknown, status = 200) =>
  new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const summary = (overrides: Partial<TariffSummary>): TariffSummary => ({
  id: 1001,
  kind: "power",
  name: "Spot 2026",
  description: null,
  status: "active",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  versionCount: 1,
  currentVersion: { validFrom: "2026-01-01", validTo: null },
  usage: { supplyPeriods: 0, meteringPoints: 0 },
  ...overrides,
});

const tariffs = [
  summary({ id: 1001, name: "Spot 2026", usage: { supplyPeriods: 3, meteringPoints: 0 } }),
  summary({
    id: 1002,
    kind: "grid",
    name: "Grid household",
    description: "Our own grid",
    currentVersion: { validFrom: "2026-01-01", validTo: "2026-07-01" },
  }),
  summary({ id: 1003, name: "Fixed 2024", status: "archived", currentVersion: null }),
];

const renderPage = (props: { canManage?: boolean }, answer?: (url: string, init?: RequestInit) => unknown) => {
  const fetchMock = stubFetch(async (input, init) => answer?.(String(input), init) ?? json(tariffs));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <TariffsPage {...props} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

const rowOf = async (name: string) => (await screen.findByText(name)).closest("tr") as HTMLElement;

describe("TariffsPage", () => {
  it("lists each tariff with its kind, status, the version valid today and its usage", async () => {
    renderPage({});
    const spot = await rowOf("Spot 2026");
    expect(within(spot).getByText("Power tariff")).toBeInTheDocument();
    expect(within(spot).getByText("Active")).toBeInTheDocument();
    expect(within(spot).getByText("From Jan 1, 2026")).toBeInTheDocument();
    expect(within(spot).getByText("In use by 3 supply periods / 0 metering points")).toBeInTheDocument();

    const grid = await rowOf("Grid household");
    expect(within(grid).getByText("Grid tariff")).toBeInTheDocument();
    expect(within(grid).getByText("Our own grid")).toBeInTheDocument();
    expect(within(grid).getByText("Jan 1, 2026 – Jul 1, 2026 (not included)")).toBeInTheDocument();
    expect(within(grid).getByText("Not in use")).toBeInTheDocument();

    const archived = await rowOf("Fixed 2024");
    expect(within(archived).getByText("Archived")).toBeInTheDocument();
    expect(within(archived).getByText("No version today")).toBeInTheDocument();
  });

  it("narrows the list by kind and status", async () => {
    const fetchMock = renderPage({});
    await screen.findByText("Spot 2026");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/energy/tariffs");

    await userEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    await userEvent.click(await screen.findByRole("option", { name: "Grid tariff" }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain("/api/v1/energy/tariffs?kind=grid"),
    );

    await userEvent.click(screen.getByRole("combobox", { name: "Status" }));
    await userEvent.click(await screen.findByRole("option", { name: "Archived" }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
        "/api/v1/energy/tariffs?kind=grid&status=archived",
      ),
    );
  });

  it("offers no writes without energy:tariffs-manage", async () => {
    renderPage({});
    await screen.findByText("Spot 2026");
    expect(screen.queryByRole("button", { name: "New tariff" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit Spot 2026" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete Grid household" })).not.toBeInTheDocument();
  });

  it("disables Delete on a tariff in use, with the reason", async () => {
    renderPage({ canManage: true });
    const remove = await screen.findByRole("button", { name: "Delete Spot 2026" });
    expect(remove).toBeDisabled();
    await userEvent.hover(remove.parentElement as HTMLElement);
    expect(await screen.findByText(/a tariff that a supply period or a metering point names cannot/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Delete Grid household" })).toBeEnabled();
    // An archived tariff is not archived again.
    expect(screen.queryByRole("button", { name: "Archive Fixed 2024" })).not.toBeInTheDocument();
  });

  it("archives a tariff once confirmed", async () => {
    const fetchMock = renderPage({ canManage: true }, (_url, init) =>
      init?.method === "POST" ? json({ ...tariffs[1], status: "archived", versions: [] }) : undefined,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Archive Grid household" }));
    const dialog = await screen.findByRole("dialog", { name: "Archive Grid household?" });
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/energy/tariffs/1002/archive",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("deletes an unused tariff once confirmed", async () => {
    const fetchMock = renderPage({ canManage: true }, (_url, init) =>
      init?.method === "DELETE" ? new Response(null, { status: 204 }) : undefined,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Delete Grid household" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Grid household?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/energy/tariffs/1002",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
  });
});
