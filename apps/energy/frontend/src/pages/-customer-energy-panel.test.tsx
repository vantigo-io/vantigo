import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerEnergyPanel } from "./customers.$customerId.energy";

const renderPanel = (props: { canAttach?: boolean }) => {
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
        <CustomerEnergyPanel customerId={1001} {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("CustomerEnergyPanel", () => {
  afterEach(cleanup);

  it("shows the attach action and empty state", async () => {
    renderPanel({ canAttach: true });

    expect(screen.getByRole("button", { name: "Attach metering point" })).toBeInTheDocument();
    expect(await screen.findByText("No metering points")).toBeInTheDocument();
  });

  it("offers no attach action unless the host says this caller may attach", async () => {
    // A merged-away customer is the case the host withholds it for; forgetting
    // the prop withholds it too.
    renderPanel({ canAttach: false });
    expect(await screen.findByText("No metering points")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Attach metering point" })).not.toBeInTheDocument();

    cleanup();
    renderPanel({});
    expect(await screen.findByText("No metering points")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Attach metering point" })).not.toBeInTheDocument();
  });
});
