import { MantineProvider } from "@mantine/core";
import { spotlight } from "@mantine/spotlight";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { i18n } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { spotlightNavSections } from "../apps";
import { type ModuleKey, visibleNavSections } from "../navigation";
import { AppSpotlight } from "./app-spotlight";

const allModules: ModuleKey[] = ["communications", "customers", "energy", "products", "projects", "time"];

const navigateMock = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useNavigate: () => navigateMock };
});

const customer = { id: 7, name: "Acme Corporation" };
const project = { id: 31, code: "ACME1000", name: "Roof replacement" };
const meteringPoint = {
  id: 12,
  gsrn: "707057500000000012",
  meterNumber: "M-1200",
  address: { streetAddress: "Storgata 1", postalCode: "0155", city: "Oslo", countryCode: "NO" },
  priceArea: "NO1",
  connectionStatus: "Connected",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};
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
  enabledModules: readonly ModuleKey[] | undefined = allModules,
) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  const rendered = render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <AppSpotlight
          permissions={permissions}
          isOwner={isOwner}
          canManageAuthorization={canManageAuthorization}
          enabledModules={enabledModules}
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
  const expected = visibleNavSections(spotlightNavSections, {
    permissions,
    isOwner,
    canManageAuthorization,
    enabledModules: allModules,
  }).flatMap((section) => section.items.map((item) => i18n.t(item.label, { ns: "host", lng: "en" })));
  const restricted = spotlightNavSections
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
    window.history.replaceState({}, "", "/");
    vi.clearAllMocks();
  });

  it("navigates to the bare destination path", async () => {
    renderSpotlight(["*"], true, true);

    fireEvent.click(await waitFor(() => screen.getByText("Customers", { exact: true })));

    expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to: "/customers" }));
  });

  it.each([
    ["Create customer", "/customers"],
    ["Add product", "/products"],
    ["Create project", "/projects"],
    // Nothing in the spotlight knows which project a new task belongs to, so
    // the action lands on My tasks, which opens the project picker first.
    ["Create task", "/projects/my-tasks"],
  ])("opens the create form from the %s quick action", async (label, to) => {
    const onNavigate = vi.fn();
    renderSpotlight(["*"], true, true, onNavigate);

    fireEvent.click(await waitFor(() => screen.getByText(label, { exact: true })));

    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to, search: { create: true } }));
  });

  // Creating a task needs no permission of its own — tasks follow the
  // project's roles — but it does need the app, so projects:access alone
  // offers it while a caller without it never sees it.
  it("offers Create task on projects:access and hides it without", async () => {
    renderSpotlight(["projects:access"], false, false);
    await waitFor(() => expect(screen.getByText("Create task", { exact: true })).toBeInTheDocument());
    expect(screen.queryByText("Create project", { exact: true })).not.toBeInTheDocument();

    cleanup();
    spotlight.close();
    renderSpotlight(["customers:view"], false, false);
    await waitFor(() => expect(screen.queryByText("Create task", { exact: true })).not.toBeInTheDocument());
  });

  // Logging time needs no more than the Time app itself, and it lands on My
  // week with no week in the URL, so the action always opens the current week.
  it("opens My week from the Log time quick action", async () => {
    const onNavigate = vi.fn();
    renderSpotlight(["time:access"], false, false, onNavigate);

    fireEvent.click(await waitFor(() => screen.getByText("Log time", { exact: true })));

    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to: "/time", search: undefined }));
  });

  it("hides Log time without time:access and when the time module is not enabled", async () => {
    renderSpotlight(["projects:access"], false, false);
    await waitFor(() => expect(screen.queryByText("Log time", { exact: true })).not.toBeInTheDocument());

    cleanup();
    spotlight.close();
    renderSpotlight(["*"], true, true, undefined, ["customers", "communications", "products", "energy", "projects"]);
    await waitFor(() => expect(screen.getByText("Create customer", { exact: true })).toBeInTheDocument());
    expect(screen.queryByText("Log time", { exact: true })).not.toBeInTheDocument();
  });

  it("hides Create task when the projects module is not enabled", async () => {
    renderSpotlight(["*"], true, true, undefined, ["customers", "communications", "products", "energy"]);

    await waitFor(() => expect(screen.getByText("Create customer", { exact: true })).toBeInTheDocument());
    expect(screen.queryByText("Create task", { exact: true })).not.toBeInTheDocument();
  });

  it("matches sidebar navigation for a permitted owner", async () => {
    renderSpotlight(["*"], true, true);

    await expectNavigationParity(["*"], true, true);
  });

  it("matches restricted sidebar navigation without exposing denied destinations", async () => {
    const permissions = ["customers:view", "communications:conversations-view"];
    renderSpotlight(permissions, false, false);

    await expectNavigationParity(permissions, false, false);
  });

  it("shows the admin dashboard in Spotlight only to owners", async () => {
    renderSpotlight(["*"], true, true);
    await waitFor(() => expect(screen.getByText("Workspace admin", { exact: true })).toBeInTheDocument());

    cleanup();
    spotlight.close();
    renderSpotlight(["*"], false, true);
    await waitFor(() => expect(screen.queryByText("Workspace admin", { exact: true })).not.toBeInTheDocument());
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
      expect(screen.queryByText("Workspace admin", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("Users", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("Invitations", { exact: true })).not.toBeInTheDocument();
    });
  });

  it("queries only entities covered by their effective permissions", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(
        new Response(
          url.includes("/api/v1/customers/contacts")
            ? JSON.stringify(paginated([]))
            : JSON.stringify(paginated([customer])),
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
    expect(
      fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/api/v1/customers/contacts")),
    ).toBe(false);
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/metering-points"))).toBe(
      false,
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/api/v1/customers?"))).toBe(
      true,
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/api/v1/projects"))).toBe(
      false,
    );
  });

  it("finds projects by code or name for a caller holding projects:access", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(
        new Response(JSON.stringify(url.includes("/api/v1/projects") ? paginated([project]) : paginated([])), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const onNavigate = vi.fn();
    renderSpotlight(["projects:access"], false, false, onNavigate);

    await enterSearch("roof");

    const label = `${project.code} — ${project.name}`;
    await waitFor(() => expect(screen.getByText(label, { exact: true })).toBeInTheDocument());
    // The package's own search query, asked for the five rows shown here.
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain("/api/v1/projects?search=roof&pageSize=5");

    fireEvent.click(screen.getByText(label, { exact: true }));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith(
      expect.objectContaining({ to: "/projects/$projectId", params: { projectId: project.id } }),
    );
  });

  it("does not search projects when the projects module is disabled", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) =>
      Promise.resolve(
        new Response(
          JSON.stringify(String(input).includes("/api/v1/projects") ? paginated([project]) : paginated([])),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderSpotlight(["*"], true, true, undefined, ["customers", "communications", "products", "energy"]);

    await enterSearch("roof");

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/api/v1/projects"))).toBe(
      false,
    );
    expect(screen.queryByText(`${project.code} — ${project.name}`, { exact: true })).not.toBeInTheDocument();
  });

  it("finds metering points by search for users with the energy view permission", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(
        new Response(
          url.includes("/metering-points") ? JSON.stringify(paginated([meteringPoint])) : JSON.stringify(paginated([])),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const onNavigate = vi.fn();
    renderSpotlight(["energy:metering-points-view"], false, false, onNavigate);

    await enterSearch("7070");

    await waitFor(() => expect(screen.getByText(meteringPoint.gsrn, { exact: true })).toBeInTheDocument());
    expect(screen.getByText("M-1200 · Storgata 1, Oslo", { exact: true })).toBeInTheDocument();

    fireEvent.click(screen.getByText(meteringPoint.gsrn, { exact: true }));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith(
      expect.objectContaining({
        to: "/energy/metering-points/$meteringPointId",
        params: { meteringPointId: meteringPoint.id },
      }),
    );
  });

  it("does not search metering points when the energy module is disabled", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(
        new Response(JSON.stringify(url.includes("/metering-points") ? paginated([meteringPoint]) : paginated([])), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderSpotlight(["*"], true, true, undefined, ["customers", "communications", "products"]);

    await enterSearch("7070");

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([input]) => String(input)).some((url) => url.includes("/metering-points"))).toBe(
      false,
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
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes("/api/v1/customers/contacts")
        ? paginated([{ contact, customer: null, customerCount: 0 }])
        : url.includes("/metering-points") || url.includes("/api/v1/projects")
          ? paginated([])
          : paginated([customer]);
      return Promise.resolve(
        new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    });
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
