import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { meta, stubAccountingApi } from "../test/server";
import { SupplierInvoicesNotice } from "./supplier-invoices-notice";

const mount = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SupplierInvoicesNotice />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("the notice Expenses shows on its supplier invoice kind", () => {
  it("points to Supplier invoices once the books are set up", async () => {
    stubAccountingApi();
    mount();
    expect(await screen.findByRole("link", { name: "Open Supplier invoices" })).toHaveAttribute(
      "href",
      "/accounting/supplier-invoices",
    );
  });

  it("says nothing before the books are set up", async () => {
    const fetchMock = stubAccountingApi({ meta: meta({ setUp: false, settings: null }) });
    mount();
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(fetchMock.actualCalls.length).toBeGreaterThan(0);
    expect(screen.queryByTestId("supplier-invoices-notice")).not.toBeInTheDocument();
  });
});
