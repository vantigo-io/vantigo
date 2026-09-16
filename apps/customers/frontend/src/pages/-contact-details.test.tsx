import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch as stubTestFetch } from "../test/fetch";
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
    pageSize: 25,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const anders = {
  id: 1001,
  firstName: "Anders",
  lastName: "Refsdal",
  middleName: null,
  prefix: "Dr.",
  suffix: null,
  phone: "+47 934 89 731",
  email: "anders@refsdal.no",
};

const stubFetch = (handlers: Record<string, (init?: RequestInit) => Response>) =>
  stubTestFetch((url: RequestInfo | URL, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${String(url).split("?")[0]}`;
    const handler = handlers[key];
    return Promise.resolve(handler ? handler(init) : new Response(null, { status: 404 }));
  });

const renderRoute = async (path: string, heading: string) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });

  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });

  render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <ModalsProvider>
          <RouterProvider router={router} />
        </ModalsProvider>
      </QueryClientProvider>
    </MantineProvider>,
  );

  // Rendering the route resolves the contact and its customer, which can exceed
  // the one-second default when the suite runs workers in parallel.
  await screen.findByRole("heading", { name: heading }, { timeout: 5000 });
};

describe("contact details page", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("shows the formatted name, id badge and contact badges", async () => {
    stubFetch({
      "GET /api/v1/customers/contacts/1001": () => jsonResponse(200, anders),
      "GET /api/v1/customers/contacts/1001/customers": () => jsonResponse(200, { data: [] }),
    });

    await renderRoute("/customers/contacts/1001", "Dr. Anders Refsdal");

    expect(await screen.findByRole("heading", { name: "Dr. Anders Refsdal" })).toBeInTheDocument();
    expect(screen.getByText("#1001")).toBeInTheDocument();
    expect(screen.getByText("+47 934 89 731")).toBeInTheDocument();
    expect(screen.getByText("anders@refsdal.no")).toBeInTheDocument();
    expect(screen.getByText(/not associated with any customers/i)).toBeInTheDocument();
  });

  it("lists associated customers with linked names and connection fallbacks", async () => {
    stubFetch({
      "GET /api/v1/customers/contacts/1001": () => jsonResponse(200, anders),
      "GET /api/v1/customers/contacts/1001/customers": () =>
        jsonResponse(200, {
          data: [
            {
              customer: { id: 2002, name: "Refsdal Holding" },
              role: "CEO",
              phone: null,
              email: "anders@refsdalholding.no",
            },
          ],
        }),
    });

    await renderRoute("/customers/contacts/1001", "Dr. Anders Refsdal");

    const link = await screen.findByRole("link", { name: "Refsdal Holding" });
    expect(link).toHaveAttribute("href", "/customers/2002");
    expect(screen.getByText("CEO")).toBeInTheDocument();
    // Connection email shown plainly, inherited phone dimmed from the contact's own.
    expect(screen.getByText("anders@refsdalholding.no")).toBeInTheDocument();
    expect(screen.getAllByText("+47 934 89 731").length).toBeGreaterThanOrEqual(2);
  });

  it("attaches the contact to a customer found through the search", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, { contact: anders, role: "CEO", phone: null, email: null }),
    );

    stubFetch({
      "GET /api/v1/customers/contacts/1001": () => jsonResponse(200, anders),
      "GET /api/v1/customers/contacts/1001/customers": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers": () =>
        jsonResponse(
          200,
          paginated([
            { id: 2002, name: "Refsdal Holding", timelineSummary: { entryCount: 0, latestOccurredOn: null } },
          ]),
        ),
      "POST /api/v1/customers/2002/contacts": attachSpy,
    });

    await renderRoute("/customers/contacts/1001", "Dr. Anders Refsdal");

    await userEvent.click(await screen.findByRole("button", { name: /add customer/i }));

    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a customer/i), "refsdal");
    await userEvent.click(await screen.findByText("Refsdal Holding"));

    await userEvent.type(within(modal).getByLabelText(/role/i), "CEO");
    await userEvent.click(within(modal).getByRole("button", { name: /^add customer$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({ contactId: 1001, role: "CEO" });
    expect(await screen.findByText("Customer added")).toBeInTheDocument();
  });

  it("shows a not-found state for unknown contacts", async () => {
    stubFetch({});

    await renderRoute("/customers/contacts/999999", "Contact not found");

    expect(await screen.findByText("Contact not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /back to contacts/i })).toBeInTheDocument();
  });
});
