import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Settlement } from "../api/settlements";
import { stubFetch } from "../test/fetch";
import { SettlementsCard } from "./-settlements-card";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const settlement = (overrides: Partial<Settlement>): Settlement => ({
  id: 1,
  revision: 1,
  supplyPeriodId: 5,
  meteringPointId: 7,
  customerId: 1002,
  contractName: "Spot 2026",
  periodStart: "2026-08-31T22:00:00Z",
  periodEnd: "2026-09-30T22:00:00Z",
  kind: "regular",
  state: "draft",
  kwh: 412.5,
  amount: 1234.5,
  currency: "NOK",
  estimated: false,
  missingIntervals: 0,
  lines: [],
  ...overrides,
});

const renderCard = (response: Settlement[] | Response) => {
  stubFetch(async () => (response instanceof Response ? response : json(response)));
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SettlementsCard meteringPointId={7} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("SettlementsCard", () => {
  it("shows the empty state when no billing period has closed", async () => {
    renderCard([]);
    expect(await screen.findByText("No billing period has closed yet.")).toBeInTheDocument();
  });

  it("shows an error when the settlements cannot be loaded", async () => {
    renderCard(new Response("{}", { status: 500, headers: { "Content-Type": "application/json" } }));
    expect(await screen.findByText("Failed to load settlements")).toBeInTheDocument();
  });

  it("shows the period as civil days with the last day inclusive, the state, kWh and amount", async () => {
    renderCard([settlement({ estimated: true })]);
    expect(await screen.findByText(/1 Sept 2026 – 30 Sept 2026|Sep 1, 2026 – Sep 30, 2026/)).toBeInTheDocument();
    expect(screen.getByText("Regular")).toBeInTheDocument();
    expect(screen.getByText("Draft")).toBeInTheDocument();
    expect(screen.getByText("Estimated")).toBeInTheDocument();
    expect(screen.getByText(/412/)).toBeInTheDocument();
    expect(screen.getByText(/1[\s ,]?234/)).toBeInTheDocument();
  });

  it("gives a blocked period's reason in words with the server's detail", async () => {
    renderCard([
      settlement({
        id: undefined,
        amount: 0,
        blocked: "price_gap",
        blockedDetail: "No price for 2026-09-14 12:00.",
      }),
    ]);
    expect(await screen.findByText(/Spot prices are missing/)).toBeInTheDocument();
    expect(screen.getByText(/No price for 2026-09-14 12:00\./)).toBeInTheDocument();
  });

  it("links the invoice number to the invoice", async () => {
    renderCard([
      settlement({
        state: "invoiced",
        invoice: { id: 88, number: 2041, issueDate: "2026-10-01", invoicedAt: "2026-10-01T08:00:00Z" },
      }),
    ]);
    const link = await screen.findByRole("link", { name: "2041" });
    expect(link).toHaveAttribute("href", "/invoices/88");
    expect(screen.getByText("Invoiced")).toBeInTheDocument();
  });

  it("marks a correction", async () => {
    renderCard([settlement({ kind: "correction", correctsId: 1, state: "released", amount: -50 })]);
    expect(await screen.findByText("Correction")).toBeInTheDocument();
    expect(screen.getByText("Released")).toBeInTheDocument();
  });

  it("expands a settlement's lines", async () => {
    renderCard([
      settlement({
        lines: [
          {
            kind: "grid_energy",
            text: "Grid energy day",
            quantity: 400,
            unit: "kWh",
            unitPrice: 0.4,
            amount: 160,
            vatHint: "standard",
          },
        ],
      }),
    ]);
    await userEvent.click(await screen.findByRole("button", { name: "Show lines" }));
    expect(screen.getByText("Grid energy day")).toBeInTheDocument();
    expect(screen.getByText("(Grid energy)")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Hide lines" }));
    expect(screen.queryByText("Grid energy day")).not.toBeInTheDocument();
  });
});
