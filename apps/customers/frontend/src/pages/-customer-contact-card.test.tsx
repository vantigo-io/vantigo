import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerContactCard } from "./-customer-contact-card";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1001,
  customerNumber: 5001,
  name: "Acme",
  status: "active",
  type: "business",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision: 3,
  contactInfo: { email: null, phone: null, website: null },
  ...overrides,
});

/**
 * The last PUT to the contact-info endpoint fetch saw (not "the last fetch":
 * addresses' own GET can land after it — see the same convention in
 * -customer-form-modal.test.tsx's own `lastPut`).
 */
const lastContactInfoPut = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls
    .filter(
      ([url, init]) => String(url).endsWith("/contact-info") && (init as RequestInit | undefined)?.method === "PUT",
    )
    .at(-1);

const stubRoutes = (
  fetchMock: ReturnType<typeof vi.fn>,
  handlers: { customer: () => unknown; contactInfoPut?: (body: unknown) => unknown },
) =>
  fetchMock.mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    if (path === "/api/v1/customers/1001" && (!init || init.method === undefined)) {
      return Promise.resolve(jsonResponse(200, handlers.customer()));
    }
    if (path === "/api/v1/customers/1001/contact-info" && init?.method === "PUT" && handlers.contactInfoPut) {
      return Promise.resolve(handlers.contactInfoPut(JSON.parse(String(init.body))));
    }
    if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderCard = (canEdit = true) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <Suspense fallback="loading">
          <CustomerContactCard customerId={1001} canEdit={canEdit} />
        </Suspense>
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("CustomerContactCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("titles the card 'Contact & addresses'", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, { customer: () => customer() });
    stubFetch(fetchMock);
    renderCard();
    expect(await screen.findByText("Contact & addresses")).toBeInTheDocument();
  });

  it("shows email as a mailto: link, phone as a tel: link and website as an external link", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, {
      customer: () =>
        customer({ contactInfo: { email: "hello@acme.test", phone: "+47 934 89 731", website: "https://acme.test" } }),
    });
    stubFetch(fetchMock);
    renderCard();

    expect(await screen.findByRole("link", { name: "hello@acme.test" })).toHaveAttribute(
      "href",
      "mailto:hello@acme.test",
    );
    expect(screen.getByRole("link", { name: "+47 934 89 731" })).toHaveAttribute("href", "tel:+4793489731");
    const website = screen.getByRole("link", { name: "https://acme.test" });
    expect(website).toHaveAttribute("href", "https://acme.test");
    expect(website).toHaveAttribute("target", "_blank");
    expect(website).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("shows an em-dash for whichever field is missing when at least one is set", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, {
      customer: () => customer({ contactInfo: { email: "hello@acme.test", phone: null, website: null } }),
    });
    stubFetch(fetchMock);
    renderCard();

    await screen.findByRole("link", { name: "hello@acme.test" });
    expect(screen.getAllByText("—")).toHaveLength(2);
  });

  it("shows the empty state with an edit action when canEdit and nothing is set", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, { customer: () => customer() });
    stubFetch(fetchMock);
    renderCard(true);

    expect(await screen.findByText("No contact details yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit contact details" })).toBeInTheDocument();
  });

  it("shows no edit affordance without canEdit", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, {
      customer: () => customer({ contactInfo: { email: "hello@acme.test", phone: null, website: null } }),
    });
    stubFetch(fetchMock);
    renderCard(false);

    await screen.findByRole("link", { name: "hello@acme.test" });
    expect(screen.queryByRole("button", { name: "Edit contact details" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Edit contact details")).not.toBeInTheDocument();
  });

  it("sends exactly {email, phone, website, revision} to PUT .../contact-info", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, {
      customer: () => customer({ contactInfo: { email: "old@acme.test", phone: null, website: null } }),
      contactInfoPut: () =>
        jsonResponse(200, customer({ contactInfo: { email: "new@acme.test", phone: null, website: null } })),
    });
    stubFetch(fetchMock);
    renderCard();

    await userEvent.click(await screen.findByLabelText("Edit contact details"));
    const dialog = await screen.findByRole("dialog");
    const emailInput = within(dialog).getByLabelText(/^email/i);
    await userEvent.clear(emailInput);
    await userEvent.type(emailInput, "new@acme.test");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(lastContactInfoPut(fetchMock)).toEqual([
      "/api/v1/customers/1001/contact-info",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "new@acme.test", phone: null, website: null, revision: 3 }),
      },
    ]);
  });

  it("shows field errors from a 400 under the right inputs", async () => {
    const fetchMock = vi.fn();
    stubRoutes(fetchMock, {
      customer: () => customer(),
      contactInfoPut: () =>
        jsonResponse(400, {
          title: "Invalid contact info",
          status: 400,
          errors: { email: ["Not a valid email address"] },
        }),
    });
    stubFetch(fetchMock);
    renderCard();

    await userEvent.click(await screen.findByRole("button", { name: "Edit contact details" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/^email/i), "not-an-email");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(await within(dialog).findByText("Not a valid email address")).toBeInTheDocument();
  });

  it("shows the Reload pattern on a revision conflict, re-seeding values and the revision the next save sends", async () => {
    const fetchMock = vi.fn();
    let reloaded = false;
    fetchMock.mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001" && (!init || init.method === undefined)) {
        return Promise.resolve(
          jsonResponse(
            200,
            reloaded
              ? customer({ contactInfo: { email: "latest@acme.test", phone: null, website: null }, revision: 4 })
              : customer({ contactInfo: { email: "old@acme.test", phone: null, website: null } }),
          ),
        );
      }
      if (path === "/api/v1/customers/1001/contact-info" && init?.method === "PUT") {
        const sent = JSON.parse(String(init.body)) as { revision?: number };
        if (sent.revision !== 4) {
          reloaded = true;
          return Promise.resolve(
            jsonResponse(409, {
              title: "Customer revision conflict",
              detail: "The customer was changed by someone else.",
              status: 409,
            }),
          );
        }
        return Promise.resolve(
          jsonResponse(
            200,
            customer({ contactInfo: { email: "latest@acme.test", phone: null, website: null }, revision: 4 }),
          ),
        );
      }
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    renderCard();

    await userEvent.click(await screen.findByLabelText("Edit contact details"));
    let dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(
      await within(dialog).findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: /reload/i }));

    dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByLabelText(/^email/i)).toHaveValue("latest@acme.test");

    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(lastContactInfoPut(fetchMock)).toEqual([
      "/api/v1/customers/1001/contact-info",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "latest@acme.test", phone: null, website: null, revision: 4 }),
      },
    ]);
  });
});
