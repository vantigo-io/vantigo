import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const paginated = (data: unknown[]) => ({
  data,
  pagination: {
    page: 1,
    pageSize: 5,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const renderApp = async (path = "/") => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });

  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <ModalsProvider>
          <RouterProvider router={router} />
        </ModalsProvider>
      </QueryClientProvider>
    </MantineProvider>,
  );

  await screen.findByRole("heading", { name: "Dashboard" });
  return router;
};

describe("app spotlight", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("opens from the sidebar search box and shows the navigation actions", async () => {
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, paginated([]))));

    await renderApp();

    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByPlaceholderText(/search customers, contacts/i)).toBeInTheDocument();
    expect(screen.getByText("Browse all customers")).toBeInTheDocument();
    expect(screen.getByText("Browse all contacts")).toBeInTheDocument();
  });

  it("navigates to the contacts page through a navigation action", async () => {
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, paginated([]))));

    const router = await renderApp();

    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    await userEvent.click(await screen.findByText("Browse all contacts"));

    await waitFor(() => expect(router.state.location.pathname).toBe("/customers/contacts"));
  });

  it("searches customers and contacts and navigates to a result", async () => {
    stubFetch((url: RequestInfo | URL) => {
      if (String(url).startsWith("/api/v1/customers?")) {
        return Promise.resolve(
          jsonResponse(
            200,
            paginated([
              {
                id: 2002,
                name: "Refsdal Holding",
                timelineSummary: { entryCount: 0, latestOccurredOn: null },
              },
            ]),
          ),
        );
      }
      if (String(url).startsWith("/api/v1/customers/contacts?")) {
        return Promise.resolve(
          jsonResponse(
            200,
            paginated([
              {
                contact: {
                  id: 1001,
                  firstName: "Anders",
                  lastName: "Refsdal",
                  middleName: null,
                  prefix: null,
                  suffix: null,
                  phone: null,
                  email: "anders@refsdal.no",
                },
                customerCount: 1,
                customer: { id: 2002, name: "Refsdal Holding" },
              },
            ]),
          ),
        );
      }
      if (url === "/api/v1/customers/2002") {
        return Promise.resolve(
          jsonResponse(200, {
            id: 2002,
            name: "Refsdal Holding",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      return Promise.resolve(jsonResponse(200, { data: [] }));
    });

    const router = await renderApp();

    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    await userEvent.type(await screen.findByPlaceholderText(/search customers, contacts/i), "refsdal");

    expect(await screen.findByText("Refsdal Holding")).toBeInTheDocument();
    expect(screen.getByText("Customer")).toBeInTheDocument();
    expect(screen.getByText("Anders Refsdal")).toBeInTheDocument();
    expect(screen.getByText("anders@refsdal.no")).toBeInTheDocument();

    await userEvent.click(screen.getByText("Refsdal Holding"));

    await waitFor(() => expect(router.state.location.pathname).toBe("/customers/2002"));
  });

  it("shows an empty state when nothing matches", async () => {
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, paginated([]))));

    await renderApp();

    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    await userEvent.type(await screen.findByPlaceholderText(/search customers, contacts/i), "zzz-no-match");

    expect(await screen.findByText("Nothing found...")).toBeInTheDocument();
  });
});
