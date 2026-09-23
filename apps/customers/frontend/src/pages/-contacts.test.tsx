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

  // The Overview tab's billing card (design D6) always GETs the billing
  // profile, which answers 200 for every customer (design D4) — with the
  // ten optional fields left out entirely when nothing is set, as the wire
  // really encodes them. Every route test below needs this stubbed the same
  // way it stubs the customer's own GET.
  const emptyBillingProfile = { revision: 1, warnings: [] };

  const stubFetch = (handlers: Record<string, (init?: RequestInit) => Response | Promise<Response>>) =>
    stubTestFetch((url: RequestInfo | URL, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${String(url).split("?")[0]}`;
      const handler = handlers[key];
      return Promise.resolve(handler ? handler(init) : new Response(null, { status: 404 }));
    });

  it("shows an empty state when the customer has no contacts", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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

    await userEvent.type(within(modal).getByLabelText(/^title$/i), "CEO");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({ contactId: 1001, title: "CEO", roles: [] });
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
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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
    await userEvent.type(within(modal).getByLabelText(/^title$/i), "Custodian");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    expect(createSpy).toHaveBeenCalled();

    const createBody = JSON.parse((createSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(createBody).toEqual({ firstName: "Nobody", lastName: "Matchesen" });

    const attachBody = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(attachBody).toEqual({ contactId: 1005, title: "Custodian", roles: [] });
  });

  it("sends the connection phone and email typed for a newly created contact, not just its own", async () => {
    // ConnectionFields renders alongside ContactFields in the create-new branch,
    // so there are two "Phone"/"Email" labelled inputs at once: the contact's
    // own (ContactFields, first) and the connection-specific one (ConnectionFields,
    // second) — this proves createAndAttach reads the second, the one
    // attachExisting has always read.
    const createSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(201, contact(1006, "Nobody", "Elsen")),
    );
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1006, "Nobody", "Elsen"),
        role: "Custodian",
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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

    await userEvent.type(within(modal).getByLabelText(/last name/i), "Elsen");
    await userEvent.type(within(modal).getByLabelText(/^title$/i), "Custodian");
    const connectionPhone = within(modal).getAllByLabelText(/^phone$/i)[1];
    await userEvent.type(connectionPhone, "+47 91 23 45 67");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const attachBody = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(attachBody).toEqual({
      contactId: 1006,
      title: "Custodian",
      roles: [],
      phone: "+47 91 23 45 67",
    });
  });

  it("shows the title under the name, a starred badge for the primary role, and the connection fallbacks", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal", { email: "anders@personal.no" }),
              role: "CEO",
              title: "CEO",
              roles: [
                { role: "billing", primary: true },
                { role: "project", primary: false },
              ],
              phone: "+47 11 22 33 44",
              email: null,
            },
            // Literally the shape the recorded corpus answers: role and
            // nothing else. The card must render it without a title line and
            // without badges, not crash on a missing array.
            { contact: contact(1002, "Kari", "Nordmann"), role: "CTO", phone: null, email: null },
          ],
        }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    expect(await screen.findByText("Anders Refsdal")).toBeInTheDocument();
    expect(screen.getByText("CEO")).toBeInTheDocument();
    expect(screen.getByLabelText("Primary billing contact")).toBeInTheDocument();
    expect(screen.getByText("Project")).toBeInTheDocument();
    expect(screen.queryByLabelText("Primary project contact")).not.toBeInTheDocument();
    // The connection fallbacks this case has always pinned: the
    // connection-specific phone plainly, the inherited email dimmed.
    expect(screen.getByText("+47 11 22 33 44")).toBeInTheDocument();
    expect(screen.getByText("anders@personal.no")).toBeInTheDocument();
    // The corpus-shaped row: its title still shows (role is the title), and it
    // has no badges of its own.
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("CTO")).toBeInTheDocument();
  });

  it("attaches with a title and the roles that were ticked, the first one primary", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "CEO",
        title: "CEO",
        roles: [{ role: "billing", primary: true }],
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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

    await userEvent.type(within(modal).getByLabelText(/^title$/i), "CEO");
    await userEvent.click(within(modal).getByRole("checkbox", { name: "Billing" }));
    await userEvent.click(within(modal).getByRole("switch", { name: "Primary billing contact" }));
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({
      contactId: 1001,
      title: "CEO",
      roles: [{ role: "billing", primary: true }],
    });
  });

  it("refuses to submit an attach with neither a title nor a role", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() => jsonResponse(200, {}));
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
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
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    expect(await within(modal).findByText(/give a title or pick at least one role/i)).toBeInTheDocument();
    expect(attachSpy).not.toHaveBeenCalled();
  });

  it("clears the stale title-or-role error the moment a role is ticked", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/contacts": () =>
        jsonResponse(
          200,
          paginated([{ contact: contact(1001, "Anders", "Refsdal"), customerCount: 0, customer: null }]),
        ),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: /add contact/i }));
    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a contact/i), "anders");
    await userEvent.click(await screen.findByText("Anders Refsdal"));
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    expect(await within(modal).findByText(/give a title or pick at least one role/i)).toBeInTheDocument();

    await userEvent.click(within(modal).getByRole("checkbox", { name: "Billing" }));

    expect(within(modal).queryByText(/give a title or pick at least one role/i)).not.toBeInTheDocument();
  });

  it("disables the Primary switch of a role the contact is the only holder of, and says why", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal"),
              role: "CEO",
              title: "CEO",
              roles: [{ role: "billing", primary: true }],
              phone: null,
              email: null,
            },
            {
              contact: contact(1002, "Kari", "Nordmann"),
              role: "CTO",
              title: "CTO",
              roles: [{ role: "project", primary: true }],
              phone: null,
              email: null,
            },
          ],
        }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: "Edit connection for Anders Refsdal" }));

    const modal = await screen.findByRole("dialog");
    const billingPrimary = within(modal).getByRole("switch", { name: "Primary billing contact" });
    expect(billingPrimary).toBeChecked();
    expect(billingPrimary).toBeDisabled();
    // Nobody else holds billing, so the reason is the specific one.
    expect(within(modal).getByText("Already the only holder")).toBeInTheDocument();
    // project is held by Kari, not by this contact, so its switch is merely
    // unticked-and-therefore-disabled, with no reason shown.
    expect(within(modal).getByRole("switch", { name: "Primary project contact" })).toBeDisabled();
    expect(within(modal).queryByText(/stays primary/i)).not.toBeInTheDocument();

    // Unticking the role is a request to drop it altogether, so the switch
    // must not keep reporting ON with "Already the only holder": that would
    // claim the primary request survives a role no longer held.
    await userEvent.click(within(modal).getByRole("checkbox", { name: "Billing" }));
    expect(within(modal).getByRole("switch", { name: "Primary billing contact" })).not.toBeChecked();
    expect(within(modal).queryByText("Already the only holder")).not.toBeInTheDocument();
  });

  it("sends the title and the complete role set when the connection is saved", async () => {
    const putSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "Chairman",
        title: "Chairman",
        roles: [
          { role: "billing", primary: true },
          { role: "decision_maker", primary: false },
        ],
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal"),
              role: "CEO",
              title: "CEO",
              roles: [{ role: "billing", primary: true }],
              phone: null,
              email: null,
            },
          ],
        }),
      "PUT /api/v1/customers/2002/contacts/1001": putSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: "Edit connection for Anders Refsdal" }));

    const modal = await screen.findByRole("dialog");
    const title = within(modal).getByLabelText(/^title$/i);
    await userEvent.clear(title);
    await userEvent.type(title, "Chairman");
    await userEvent.click(within(modal).getByRole("checkbox", { name: "Decision maker" }));
    await userEvent.click(within(modal).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(putSpy).toHaveBeenCalled());
    const body = JSON.parse((putSpy.mock.calls[0][0] as RequestInit).body as string);
    // billing carries `primary: true` because its switch is on (it is the
    // contact's primary billing role); decision_maker carries no `primary` key
    // at all, because an omitted flag is what "I did not ask about this one"
    // means on the wire — sending `false` would be the server's one refusal.
    expect(body).toEqual({
      title: "Chairman",
      roles: [{ role: "billing", primary: true }, { role: "decision_maker" }],
    });
  });

  it("round-trips a held role outside the vocabulary instead of dropping it on save", async () => {
    // The vocabulary is a value change on the server, not a migration (design
    // D2), so a contact can already hold a role this frontend's catalog does
    // not know — `executive_sponsor` here. Nothing in the UI offers a checkbox
    // for it, so it must never be lost from a complete-set replace just because
    // it was not re-ticked.
    const putSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "Chairman",
        title: "Chairman",
        roles: [
          { role: "billing", primary: true },
          { role: "executive_sponsor", primary: true },
        ],
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal"),
              role: "CEO",
              title: "CEO",
              roles: [
                { role: "billing", primary: true },
                { role: "executive_sponsor", primary: true },
              ],
              phone: null,
              email: null,
            },
          ],
        }),
      "PUT /api/v1/customers/2002/contacts/1001": putSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: "Edit connection for Anders Refsdal" }));

    const modal = await screen.findByRole("dialog");
    const title = within(modal).getByLabelText(/^title$/i);
    await userEvent.clear(title);
    await userEvent.type(title, "Chairman");
    await userEvent.click(within(modal).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(putSpy).toHaveBeenCalled());
    const body = JSON.parse((putSpy.mock.calls[0][0] as RequestInit).body as string);
    // billing is offered by a checkbox and is this contact's primary billing
    // role, so it round-trips with `primary: true`; executive_sponsor has no
    // checkbox at all, so it round-trips too, but with `primary` omitted —
    // "unchanged" is the only thing the UI can honestly say about it.
    expect(body).toEqual({
      title: "Chairman",
      roles: [{ role: "billing", primary: true }, { role: "executive_sponsor" }],
    });
  });
});
