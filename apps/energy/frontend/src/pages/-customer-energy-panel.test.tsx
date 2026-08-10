import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerEnergyPanel } from "./customers.$customerId.energy";

describe("CustomerEnergyPanel", () => {
  it("shows the attach action and empty state", async () => {
    stubFetch((url) =>
      String(url) === "/api/v1/energy/customers/1001/metering-points"
        ? Promise.resolve(
            new Response(JSON.stringify([]), { status: 200, headers: { "Content-Type": "application/json" } }),
          )
        : Promise.resolve(new Response(null, { status: 404 })),
    );

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerEnergyPanel customerId={1001} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(screen.getByRole("button", { name: "Attach metering point" })).toBeInTheDocument();
    expect(await screen.findByText("No metering points")).toBeInTheDocument();
  });
});
