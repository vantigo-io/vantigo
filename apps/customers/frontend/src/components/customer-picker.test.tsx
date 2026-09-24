import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerPicker } from "./customer-picker";

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });

// Literally what the list answers: only the merged-away one carries mergedInto.
const wire = (id: number, customerNumber: number, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  customerNumber,
  name,
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-06-01T10:00:00Z",
  timelineSummary: { entryCount: 0 },
  ...extra,
});
const page = {
  data: [
    wire(1002, 2, "Acme AS"),
    wire(1005, 5, "Acme Norge AS", { status: "archived" }),
    wire(1007, 7, "Acme Gammel AS", {
      status: "archived",
      mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" },
    }),
  ],
  pagination: { page: 1, pageSize: 20, totalCount: 3, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
};

afterEach(() => vi.unstubAllGlobals());

describe("CustomerPicker", () => {
  it("searches archived customers too, and offers neither the customer it is for nor one merged away", async () => {
    const fetchMock = vi.fn<(url: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
      Promise.resolve(jsonResponse(page)),
    );
    stubFetch(fetchMock);
    const onChange = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={queryClient}>
          <CustomerPicker label="Duplicate" placeholder="Search" value={null} onChange={onChange} excludeId={1002} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await userEvent.click(screen.getByRole("combobox", { name: "Duplicate" }));
    const option = await screen.findByRole("option", { name: "#5 Acme Norge AS · Archived" });

    expect(screen.queryByRole("option", { name: "#2 Acme AS" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Acme Gammel AS/ })).not.toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([url]) => String(url) === "/api/v1/customers?page=1&pageSize=20&includeArchived=true"),
    ).toBe(true);

    await userEvent.click(option);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1005, name: "Acme Norge AS", mergedInto: null }),
    );
  });
});
