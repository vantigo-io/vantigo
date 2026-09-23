import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerFormModal } from "./-customer-form-modal";

/**
 * The last PUT the form sent, as fetch saw it. Not "the last fetch": the form's
 * debounced Brreg lookup for the name runs on its own clock, and on a slow
 * runner it lands after the save — so the last call overall is whichever of
 * the two lost the race, which says nothing about what was saved.
 */
const lastPut = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === "PUT").at(-1);

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const renderModal = (state: Parameters<typeof CustomerFormModal>[0]["state"], onClose = vi.fn()) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  );
  render(<CustomerFormModal state={state} onClose={onClose} />, { wrapper });
  return { onClose };
};

/**
 * Renders the modal under a real router, the way the sibling tests that
 * render `Link`s do (see `src/test/route-tree.tsx`) — needed by the
 * duplicate-identity and similar-names links, which navigate through the
 * package's router-Link convention rather than a plain `<a href>`, so a
 * click here is a real assertion of client-side navigation, not a full
 * page reload.
 */
const renderModalWithRouter = async (state: Parameters<typeof CustomerFormModal>[0]["state"], onClose = vi.fn()) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <Notifications />
        <QueryClientProvider client={queryClient}>
          <CustomerFormModal state={state} onClose={onClose} />
        </QueryClientProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("dialog");
  return { onClose, router };
};

describe("CustomerFormModal", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("creates a business by default, with only the safe basic payload", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1001 }));
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    expect(screen.getByRole("radio", { name: /business/i })).toBeChecked();
    await userEvent.type(screen.getByLabelText(/name/i), "  Acme  ");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Acme", status: "active", type: "business" }),
    });
  });
  it("creates a private customer without ever looking the name up", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers") return Promise.resolve(jsonResponse(201, { id: 1002 }));
      if (path.includes("/lookup/brreg"))
        return Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "KARI NORDMANN" }] }));
      return Promise.resolve(
        jsonResponse(200, {
          data: [],
          pagination: {
            page: 1,
            pageSize: 3,
            totalCount: 0,
            totalPages: 0,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      );
    });
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));
    expect(screen.queryByText(/brønnøysundregistrene/i)).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/name/i), "Kari Nordmann");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([url]) => String(url)).some((url) => url.includes("/lookup/brreg"))).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Kari Nordmann", status: "active", type: "person" }),
    });
  });
  it("drops a picked business identity when switching to private", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers") return Promise.resolve(jsonResponse(201, { id: 1003 }));
      if (path.includes("/lookup/brreg"))
        return Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] }));
      return Promise.resolve(
        jsonResponse(200, {
          data: [],
          pagination: {
            page: 1,
            pageSize: 3,
            totalCount: 0,
            totalPages: 0,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      );
    });
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "Equinor");
    await userEvent.click(await screen.findByRole("option", { name: /equinor asa/i }));
    expect(screen.getByLabelText(/name/i)).toHaveValue("EQUINOR ASA");
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "EQUINOR ASA", status: "active", type: "person" }),
    });
  });
  it("does not render aggregate legal identity fields", () => {
    stubFetch(vi.fn());
    renderModal({ mode: "create" });
    expect(screen.queryByLabelText(/legal name/i)).not.toBeInTheDocument();
    expect(screen.getByText(/legal identity is managed separately/i)).toBeInTheDocument();
  });
  it("edits without a type toggle and never sends the type", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        jsonResponse(200, { id: 1001, name: "Initrode", timelineSummary: { entryCount: 0, latestOccurredOn: null } }),
      );
    stubFetch(fetchMock);
    const { onClose } = renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
      },
    });
    expect(screen.queryByRole("radio", { name: /business/i })).not.toBeInTheDocument();
    const input = screen.getByLabelText(/name/i);
    await userEvent.clear(input);
    await userEvent.type(input, "Initrode");
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initrode", status: "active" }),
    });
  });
  it("sends the customer's revision along with an update", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        id: 1001,
        name: "Initrode",
        status: "active",
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
      }),
    );
    stubFetch(fetchMock);
    const { onClose } = renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
        revision: 3,
      },
    });
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initech", status: "active", revision: 3 }),
    });
  });

  it("shows a revision-conflict alert, and Reload makes the next save send the fresh revision", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        // The server's side of the revision guard: the stale revision the
        // modal opened with is refused, the one Reload just read through.
        // Without a modal-local revision the second save would repeat the
        // first one's body and 409 forever.
        const sent = JSON.parse(String(init?.body)) as { revision?: number };
        if (sent.revision !== 4) {
          return Promise.resolve(
            jsonResponse(409, {
              title: "Customer revision conflict",
              detail: "The customer was changed by someone else.",
              status: 409,
            }),
          );
        }
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Initech Latest",
            status: "active",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      if (path === "/api/v1/customers/1001") {
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            customerNumber: 5001,
            name: "Initech Latest",
            status: "active",
            type: "business",
            identity: null,
            createdAt: "2026-01-01T00:00:00Z",
            updatedAt: "2026-02-01T00:00:00Z",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
            revision: 4,
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose } = renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
        revision: 3,
      },
    });
    const input = screen.getByLabelText(/name/i);
    await userEvent.clear(input);
    await userEvent.type(input, "My Own Edit");
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));

    expect(
      await screen.findByText("This customer was changed by someone else. Reload to see the latest version."),
    ).toBeInTheDocument();
    expect(screen.getByText(/your changes have not been saved/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /reload/i }));

    expect(await screen.findByLabelText(/name/i)).toHaveValue("Initech Latest");
    expect(
      screen.queryByText("This customer was changed by someone else. Reload to see the latest version."),
    ).not.toBeInTheDocument();

    // The point of Reload: the next save works. The modal-state prop still
    // carries revision 3 (the list row it was opened from is not re-derived),
    // so the revision the mutation sends has to come from what Reload read.
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(lastPut(fetchMock)).toEqual([
      "/api/v1/customers/1001",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Initech Latest", status: "active", revision: 4 }),
      },
    ]);
    expect(
      screen.queryByText("This customer was changed by someone else. Reload to see the latest version."),
    ).not.toBeInTheDocument();
  });

  it("shows a duplicate-identity conflict on update and resubmits with allowDuplicateIdentity on Save anyway", async () => {
    let attempts = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        attempts += 1;
        if (attempts === 1) {
          return Promise.resolve(
            jsonResponse(409, {
              title: "Duplicate legal identity",
              code: "duplicate_legal_identity",
              detail: "Another customer already has this legal identity.",
              status: 409,
              duplicates: [{ id: 2002, customerNumber: 6002, name: "Acme Holding AS", status: "active" }],
            }),
          );
        }
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Initech",
            status: "active",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose } = await renderModalWithRouter({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
        revision: 3,
      },
    });
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));

    expect(await screen.findByText("Acme Holding AS")).toBeInTheDocument();
    expect(screen.getByText("#6002")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /acme holding as/i })).toHaveAttribute("href", "/customers/2002");

    await userEvent.click(screen.getByRole("button", { name: /save anyway/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(lastPut(fetchMock)).toEqual([
      "/api/v1/customers/1001",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Initech", status: "active", revision: 3, allowDuplicateIdentity: true }),
      },
    ]);
  });

  it("shows the duplicate alert and its anyway button even when the conflict names nobody", async () => {
    // A caller without customers:view gets the conflict without the duplicates
    // list (the server withholds it — see duplicates.go). The alert still has
    // everything the caller can act on: what went wrong, and the override.
    let attempts = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        attempts += 1;
        if (attempts === 1) {
          return Promise.resolve(
            jsonResponse(409, {
              title: "Duplicate legal identity",
              code: "duplicate_legal_identity",
              detail: "Another customer already has this legal identity.",
              status: 409,
            }),
          );
        }
        return Promise.resolve(
          jsonResponse(200, {
            id: 1001,
            name: "Initech",
            status: "active",
            timelineSummary: { entryCount: 0, latestOccurredOn: null },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose } = await renderModalWithRouter({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
        revision: 3,
      },
    });
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));

    expect(await screen.findByText(/another customer already has this legal identity/i)).toBeInTheDocument();
    expect(screen.queryAllByRole("link")).toHaveLength(0);

    await userEvent.click(screen.getByRole("button", { name: /save anyway/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(lastPut(fetchMock)).toEqual([
      "/api/v1/customers/1001",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Initech", status: "active", revision: 3, allowDuplicateIdentity: true }),
      },
    ]);
  });

  it("clicking a duplicate's link navigates through the router (no full page reload) and closes the modal", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001" && init?.method === "PUT") {
        return Promise.resolve(
          jsonResponse(409, {
            title: "Duplicate legal identity",
            code: "duplicate_legal_identity",
            detail: "Another customer already has this legal identity.",
            status: 409,
            duplicates: [{ id: 2002, customerNumber: 6002, name: "Acme Holding AS", status: "active" }],
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose, router } = await renderModalWithRouter({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        tags: [],
        revision: 3,
      },
    });
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    const link = await screen.findByRole("link", { name: /acme holding as/i });

    await userEvent.click(link);

    // A plain <a href> click would leave the router's own location alone;
    // the router-Link intercepts the click and navigates client-side.
    expect(router.state.location.pathname).toBe("/customers/2002");
    expect(onClose).toHaveBeenCalled();
  });

  it("shows a duplicate-identity conflict on create and resubmits with allowDuplicateIdentity on Create anyway", async () => {
    let attempts = 0;
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers" && init?.method === "POST") {
        attempts += 1;
        if (attempts === 1) {
          return Promise.resolve(
            jsonResponse(409, {
              title: "Duplicate legal identity",
              code: "duplicate_legal_identity",
              detail: "Another customer already has this legal identity.",
              status: 409,
              duplicates: [{ id: 3003, customerNumber: 7003, name: "Kari Nordmann", status: "archived" }],
            }),
          );
        }
        return Promise.resolve(jsonResponse(201, { id: 3010 }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose } = await renderModalWithRouter({ mode: "create" });
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));
    await userEvent.type(screen.getByLabelText(/name/i), "Kari Nordmann");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /create anyway/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts.at(-1)).toEqual([
      "/api/v1/customers",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Kari Nordmann", status: "active", type: "person", allowDuplicateIdentity: true }),
      },
    ]);
  });

  it("sends contactInfo with trimmed email and phone when either is filled", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1004 }));
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "Acme");
    await userEvent.type(screen.getByLabelText(/^email/i), "  hello@acme.test  ");
    await userEvent.type(screen.getByLabelText(/^phone/i), "  +47 934 89 731  ");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Acme",
        status: "active",
        type: "business",
        contactInfo: { email: "hello@acme.test", phone: "+47 934 89 731" },
      }),
    });
  });

  it("sends only the filled half of contactInfo", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1005 }));
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "Acme");
    await userEvent.type(screen.getByLabelText(/^email/i), "hello@acme.test");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Acme",
        status: "active",
        type: "business",
        contactInfo: { email: "hello@acme.test" },
      }),
    });
  });

  it("maps a 400 keyed contactInfo.email/contactInfo.phone onto the email/phone inputs", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(400, {
        title: "Invalid customer",
        status: 400,
        errors: {
          "contactInfo.email": ["Not a valid email address"],
          "contactInfo.phone": ["Not a valid phone number"],
        },
      }),
    );
    stubFetch(fetchMock);
    renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "Acme");
    await userEvent.type(screen.getByLabelText(/^email/i), "not-an-email");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    expect(await screen.findByText("Not a valid email address")).toBeInTheDocument();
    expect(screen.getByText("Not a valid phone number")).toBeInTheDocument();
  });

  it("hints at existing customers with a similar name while creating (debounced, 3+ characters)", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path.startsWith("/api/v1/customers?")) {
        return Promise.resolve(
          jsonResponse(200, {
            data: [
              { id: 9001, customerNumber: 8001, name: "Acme Holding", status: "active" },
              { id: 9002, customerNumber: 8002, name: "Widgets Inc", status: "active" },
            ],
            pagination: {
              page: 1,
              pageSize: 3,
              totalCount: 2,
              totalPages: 1,
              hasNextPage: false,
              hasPreviousPage: false,
            },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);
    const { onClose, router } = await renderModalWithRouter({ mode: "create" });
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));

    await userEvent.type(screen.getByLabelText(/name/i), "Ac");
    expect(screen.queryByText("Acme Holding")).not.toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/name/i), "me");

    expect(await screen.findByText("Acme Holding", {}, { timeout: 3000 })).toBeInTheDocument();
    // The API search also matches contacts and org numbers; only the name match renders.
    expect(screen.queryByText("Widgets Inc")).not.toBeInTheDocument();
    const call = fetchMock.mock.calls.find(([url]) => String(url).includes("search=Acme"));
    expect(call).toBeDefined();

    const link = screen.getByRole("link", { name: "Acme Holding" });
    expect(link).toHaveAttribute("href", "/customers/9001");
    await userEvent.click(link);

    // Same router-Link convention as the duplicate list above: a real
    // client-side navigation, and the modal closes rather than staying open
    // over the customer the caller just navigated to.
    expect(router.state.location.pathname).toBe("/customers/9001");
    expect(onClose).toHaveBeenCalled();
  });
});
