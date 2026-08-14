import { MantineProvider } from "@mantine/core";
import { spotlight } from "@mantine/spotlight";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { i18n } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { navSections, visibleNavSections } from "../navigation";
import { AppSpotlight } from "./app-spotlight";

const navigateMock = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useNavigate: () => navigateMock };
});

const customer = { id: 7, name: "Acme Corporation" };
const contact = {
  id: 8,
  firstName: "Ada",
  lastName: "Lovelace",
  middleName: null,
  prefix: null,
  suffix: null,
  email: "ada@acme.test",
  phone: null,
};

const paginated = (data: unknown[]) => ({ data, total: data.length, page: 1, pageSize: 5, totalPages: 1 });

const renderSpotlight = (
  permissions: string[] | undefined,
  isOwner: boolean,
  canManageAuthorization: boolean,
  onNavigate?: () => void,
) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  const rendered = render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <AppSpotlight
          permissions={permissions}
          isOwner={isOwner}
          canManageAuthorization={canManageAuthorization}
          onNavigate={onNavigate}
        />
      </QueryClientProvider>
    </MantineProvider>,
  );
  spotlight.open();
  return { ...rendered, queryClient };
};

const expectNavigationParity = async (
  permissions: string[] | undefined,
  isOwner: boolean,
  canManageAuthorization: boolean,
) => {
  const expected = visibleNavSections(permissions, isOwner, canManageAuthorization).flatMap((section) =>
    section.items.map((item) => i18n.t(item.label, { ns: "host", lng: "en" })),
  );
  const restricted = navSections
    .flatMap((section) => section.items)
    .map((item) => i18n.t(item.label, { ns: "host", lng: "en" }))
    .filter((label) => !expected.includes(label));

  await waitFor(() => {
    for (const label of expected) expect(screen.getByText(label, { exact: true })).toBeInTheDocument();
    for (const label of restricted) expect(screen.queryByText(label, { exact: true })).not.toBeInTheDocument();
  });
};

const enterSearch = async (value: string) => {
  const input = await waitFor(() => screen.getByPlaceholderText("Search customers, contacts..."));
  fireEvent.change(input, { target: { value } });
};

describe("AppSpotlight navigation authorization", () => {
  afterEach(() => {
    cleanup();
    spotlight.close();
    vi.clearAllMocks();
  });

  it("matches sidebar navigation for a permitted owner", async () => {
    renderSpotlight(["*"], true, true);

    await expectNavigationParity(["*"], true, true);
  });

  it("matches restricted sidebar navigation without exposing denied destinations", async () => {
    const permissions = ["customers:view", "communications:messages-view"];
    renderSpotlight(permissions, false, false);

    await expectNavigationParity(permissions, false, false);
  });

  it("shows the admin dashboard in Spotlight only to owners", async () => {
    renderSpotlight(["*"], true, true);
    await waitFor(() => expect(screen.getByText("Admin dashboard", { exact: true })).toBeInTheDocument());

    cleanup();
    spotlight.close();
    renderSpotlight(["*"], false, true);
    await waitFor(() => expect(screen.queryByText("Admin dashboard", { exact: true })).not.toBeInTheDocument());
  });

  it("does not expose users or invitations in owner search and keeps Roles & access for authorization users", async () => {
    renderSpotlight(["*"], true, true);

    await waitFor(() => {
      expect(screen.queryByText("Users", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("Invitations", { exact: true })).not.toBeInTheDocument();
    });

    cleanup();
    spotlight.close();
    renderSpotlight(["*"], false, true);

    await waitFor(() => {
      expect(screen.getByText("Roles & access", { exact: true })).toBeInTheDocument();
      expect(screen.queryByText("Admin dashboard", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("Users", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("Invitations", { exact: true })).not.toBeInTheDocument();
    });
  });

  it("queries only entities covered by their effective permissions", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(
        new Response(
          url.includes("/contacts") ? JSON.stringify(paginated([])) : JSON.stringify(paginated([customer])),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderSpotlight(["customers:view"], false, false);

    await enterSearch("acme");

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/contacts"))).toBe(false);
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/api/v1/customers?"))).toBe(
      true,
    );
  });

  it("hides cached entity results immediately when permission is removed", async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(paginated([customer])), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const rendered = renderSpotlight(["customers:view"], false, false);
    await enterSearch("acme");

    await waitFor(() => expect(screen.getByText(customer.name, { exact: true })).toBeInTheDocument());
    rendered.rerender(
      <MantineProvider>
        <QueryClientProvider client={rendered.queryClient ? rendered.queryClient : new QueryClient()}>
          <AppSpotlight permissions={[]} isOwner={false} canManageAuthorization={false} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(screen.queryByText(customer.name, { exact: true })).not.toBeInTheDocument();
  });

  it("invokes onNavigate for navigation, customer, and contact actions", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) =>
      Promise.resolve(
        new Response(
          String(input).includes("/contacts")
            ? JSON.stringify(paginated([{ contact, customer: null, customerCount: 0 }]))
            : JSON.stringify(paginated([customer])),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const onNavigate = vi.fn();
    renderSpotlight(["*"], true, true, onNavigate);

    const customersAction = await waitFor(() => screen.getByText("Customers", { exact: true }));
    fireEvent.click(customersAction);
    expect(onNavigate).toHaveBeenCalledTimes(1);

    spotlight.open();
    await enterSearch("acme");
    await waitFor(() => expect(screen.getByText(customer.name, { exact: true })).toBeInTheDocument());
    fireEvent.click(screen.getByText(customer.name, { exact: true }));
    expect(onNavigate).toHaveBeenCalledTimes(2);

    spotlight.open();
    await enterSearch("ada");
    await waitFor(() => expect(screen.getByText("Ada Lovelace", { exact: true })).toBeInTheDocument());
    fireEvent.click(screen.getByText("Ada Lovelace", { exact: true }));
    expect(onNavigate).toHaveBeenCalledTimes(3);
  });
});
