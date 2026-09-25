import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CustomerResponse } from "../api/customers";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";
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
    // Anonymised (customers GDPR design D4): nothing of a person is left to
    // fold in, and the merge refuses it.
    wire(1009, 9, "Anonymised person", {
      status: "archived",
      type: "person",
      anonymisation: { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" },
    }),
  ],
  pagination: { page: 1, pageSize: 20, totalCount: 4, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
};

afterEach(() => vi.unstubAllGlobals());

describe("CustomerPicker", () => {
  it("searches archived customers too, and offers neither the customer it is for nor one merged away or anonymised", async () => {
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
    expect(screen.queryByRole("option", { name: /Anonymised person/ })).not.toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([url]) => String(url) === "/api/v1/customers?page=1&pageSize=20&includeArchived=true"),
    ).toBe(true);

    await userEvent.click(option);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1005, name: "Acme Norge AS", mergedInto: null }),
    );
  });

  it("searches only on typing: after a pick, reopening offers the list again, not the pick alone", async () => {
    // Any search answers nothing, so a search for the picked label would leave
    // only the pick on offer.
    const fetchMock = vi.fn<(url: RequestInfo | URL, init?: RequestInit) => Promise<Response>>((url) =>
      Promise.resolve(
        jsonResponse(
          String(url).includes("search=")
            ? { ...page, data: [], pagination: { ...page.pagination, totalCount: 0 } }
            : page,
        ),
      ),
    );
    stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const Picker = () => {
      const [value, setValue] = useState<CustomerResponse | null>(null);
      return (
        <CustomerPicker label="Duplicate" placeholder="Search" value={value} onChange={setValue} excludeId={1009} />
      );
    };
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={queryClient}>
          <Picker />
        </QueryClientProvider>
      </MantineProvider>,
    );

    const input = screen.getByRole("combobox", { name: "Duplicate" });
    await userEvent.click(input);
    await userEvent.click(await screen.findByRole("option", { name: "#5 Acme Norge AS · Archived" }));
    expect(input).toHaveValue("#5 Acme Norge AS · Archived");
    // Past the debounce, so a search for the label would have been sent by now.
    await new Promise((resolve) => setTimeout(resolve, SEARCH_DEBOUNCE_MS + 200));

    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes("search="))).toEqual([]);
    await userEvent.click(input);
    expect(await screen.findByRole("option", { name: "#2 Acme AS" })).toBeInTheDocument();

    // Typing still searches.
    await userEvent.clear(input);
    await userEvent.type(input, "Norge");
    await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes("search=Norge"))).toBe(true));
  });
});
