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

const contact = (id: number, firstName: string, lastName: string, extra: object = {}) => ({
  id,
  firstName,
  lastName,
  middleName: null,
  prefix: null,
  suffix: null,
  phone: null,
  email: null,
  ...extra,
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

  await screen.findByRole("heading", { name: heading });
};

describe("contacts page", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("lists contacts with a linked customer name for single associations and a count otherwise", async () => {
    stubTestFetch((url: RequestInfo | URL) => {
      if (String(url).startsWith("/api/v1/customers/contacts")) {
        return Promise.resolve(
          jsonResponse(
            200,
            paginated([
              {
                contact: contact(1001, "Anders", "Refsdal", {
                  prefix: "Dr.",
                  phone: "+47 934 89 731",
                  email: "anders@refsdal.no",
                }),
                customerCount: 1,
                customer: { id: 2002, name: "Refsdal Holding" },
              },
              {
                contact: contact(1002, "Kari", "Nordmann"),
                customerCount: 3,
                customer: null,
              },
              {
                contact: contact(1003, "Ola", "Nordmann"),
                customerCount: 0,
                customer: null,
              },
            ]),
          ),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/customers/contacts", "Contacts");

    expect(await screen.findByText("Dr. Anders Refsdal")).toBeInTheDocument();
    expect(screen.getByText("+47 934 89 731")).toBeInTheDocument();
    expect(screen.getByText("anders@refsdal.no")).toBeInTheDocument();

    const customerLink = screen.getByRole("link", { name: "Refsdal Holding" });
    expect(customerLink).toHaveAttribute("href", "/customers/2002");

    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("0")).toBeInTheDocument();
  });
});

describe("customer contacts card", () => {
  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  const customer = {
    id: 2002,
    name: "Refsdal Holding",
    status: "active",
    createdAt: "2026-06-01T10:00:00Z",
    updatedAt: "2026-07-01T10:00:00Z",
    identity: null,
    timelineSummary: { entryCount: 0, latestOccurredOn: null },
  };

  const stubFetch = (handlers: Record<string, (init?: RequestInit) => Response | Promise<Response>>) =>
    stubTestFetch((url: RequestInfo | URL, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${String(url).split("?")[0]}`;
      const handler = handlers[key];
      return Promise.resolve(handler ? handler(init) : new Response(null, { status: 404 }));
    });

  it("lists associated contacts with connection values falling back to the contact's own", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal", { email: "anders@personal.no" }),
              role: "CEO",
              phone: "+47 11 22 33 44",
              email: null,
            },
          ],
        }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    expect(await screen.findByText("Anders Refsdal")).toBeInTheDocument();
    expect(screen.getByText("CEO")).toBeInTheDocument();
    // Connection-specific phone is shown plainly, the inherited email dimmed.
    expect(screen.getByText("+47 11 22 33 44")).toBeInTheDocument();
    expect(screen.getByText("anders@personal.no")).toBeInTheDocument();
  });

  it("shows an empty state when the customer has no contacts", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    expect(await screen.findByText(/no contacts associated/i)).toBeInTheDocument();
  });

  it("attaches an existing contact found through the search", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "CEO",
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/contacts": () =>
        jsonResponse(
          200,
          paginated([{ contact: contact(1001, "Anders", "Refsdal"), customerCount: 0, customer: null }]),
        ),
      "POST /api/v1/customers/2002/contacts": attachSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    await userEvent.click(await screen.findByRole("button", { name: /add contact/i }));

    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a contact/i), "anders");
    await userEvent.click(await screen.findByText("Anders Refsdal"));

    await userEvent.type(within(modal).getByLabelText(/role/i), "CEO");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({ contactId: 1001, role: "CEO" });
    expect(await screen.findByText("Contact added")).toBeInTheDocument();
  });

  it("offers to create a new contact when the search finds nothing", async () => {
    const createSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(201, contact(1005, "Nobody", "Matchesen")),
    );
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1005, "Nobody", "Matchesen"),
        role: "Custodian",
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/contacts": () => jsonResponse(200, paginated([])),
      "POST /api/v1/customers/contacts": createSpy,
      "POST /api/v1/customers/2002/contacts": attachSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    await userEvent.click(await screen.findByRole("button", { name: /add contact/i }));

    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a contact/i), "Nobody");
    await userEvent.click(await screen.findByText(/no contact found/i));

    // The first name is prefilled from the search text.
    expect(within(modal).getByLabelText(/first name/i)).toHaveValue("Nobody");

    await userEvent.type(within(modal).getByLabelText(/last name/i), "Matchesen");
    await userEvent.type(within(modal).getByLabelText(/role/i), "Custodian");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    expect(createSpy).toHaveBeenCalled();

    const createBody = JSON.parse((createSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(createBody).toEqual({ firstName: "Nobody", lastName: "Matchesen" });

    const attachBody = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(attachBody).toEqual({ contactId: 1005, role: "Custodian" });
  });
});
