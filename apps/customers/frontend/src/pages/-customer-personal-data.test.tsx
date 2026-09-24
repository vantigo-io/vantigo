import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { saveCsv } from "../api/import-export";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader, CustomerOverview } from "./customers.$customerId";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));
vi.mock("../api/import-export", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/import-export")>()),
  saveCsv: vi.fn(),
}));

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status < 300 ? "application/json" : "application/problem+json" },
  });

// Literally the body the server sends for a private person with nothing set.
const person = (overrides: Record<string, unknown> = {}) => ({
  id: 1005,
  customerNumber: 5,
  name: "Kari Nordmann",
  status: "active",
  type: "person",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 0 },
  revision: 4,
  ...overrides,
});

/** A day `days` from today in UTC, the calendar the server counts in. */
const utcDay = (days: number) => new Date(Date.now() + days * 86_400_000).toISOString().slice(0, 10);

type Handler = (url: string, init?: RequestInit) => Response | undefined;
type FetchMock = ReturnType<typeof vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>>;

const renderHeader = async (
  body: ReturnType<typeof person>,
  props: { canManagePersonalData?: boolean; canRestore?: boolean; canMerge?: boolean } = {},
  handle: Handler = () => undefined,
) => {
  const fetchMock: FetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const answer = handle(url, init);
    if (answer) return Promise.resolve(answer);
    if (url === "/api/v1/customers/1005/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
    if (url === "/api/v1/customers/1005") return Promise.resolve(json(200, body));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerDetailHeader customerId={1005} {...props} />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("heading", { name: body.name }, { timeout: 5000 });
  return fetchMock;
};

const callsTo = (fetchMock: FetchMock, method: string, url: string) =>
  fetchMock.mock.calls.filter(([u, init]) => String(u) === url && (init?.method ?? "GET") === method);

const openMenu = async () => userEvent.click(screen.getByRole("button", { name: "Personal data" }));

describe("customer detail header — personal data", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("offers Personal data on a private person's page only, and only when the host says so", async () => {
    await renderHeader(person());
    expect(screen.queryByRole("button", { name: "Personal data" })).not.toBeInTheDocument();
    cleanup();
    await renderHeader(person({ type: "business", name: "Acme AS" }), { canManagePersonalData: true });
    expect(screen.queryByRole("button", { name: "Personal data" })).not.toBeInTheDocument();
    cleanup();
    await renderHeader(person(), { canManagePersonalData: true });
    expect(screen.getByRole("button", { name: "Personal data" })).toBeInTheDocument();
  });

  it("downloads the file under the name the server gave it", async () => {
    const fetchMock = await renderHeader(person(), { canManagePersonalData: true }, (url) =>
      url === "/api/v1/customers/1005/personal-data"
        ? new Response("{}", {
            status: 200,
            headers: { "Content-Disposition": 'attachment; filename="customer-5-personal-data.json"' },
          })
        : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Export personal data" }));
    await vi.waitFor(() =>
      expect(saveCsv).toHaveBeenCalledWith(expect.objectContaining({ fileName: "customer-5-personal-data.json" })),
    );
    expect(callsTo(fetchMock, "GET", "/api/v1/customers/1005/personal-data")).toHaveLength(1);
  });

  it("keeps scheduling off, with the reason, while the customer is active", async () => {
    await renderHeader(person(), { canManagePersonalData: true });
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" })).toBeDisabled();
    expect(
      screen.getByText("Archive the customer first: an ongoing relationship is not anonymised."),
    ).toBeInTheDocument();
  });

  it("schedules an archived person on the day chosen, and sends that day", async () => {
    const day = utcDay(40);
    const fetchMock = await renderHeader(
      person({ status: "archived" }),
      { canManagePersonalData: true },
      (url, init) =>
        url === "/api/v1/customers/1005/anonymisation" && init?.method === "PUT"
          ? json(200, person({ status: "archived", revision: 5, anonymisation: { anonymiseOn: day } }))
          : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" }));
    const dialog = await screen.findByRole("dialog", { name: "Schedule anonymisation" });
    expect(within(dialog).getByText("An anonymisation cannot be undone.")).toBeInTheDocument();
    await userEvent.type(within(dialog).getByRole("textbox", { name: /anonymise on/i }), day);
    await userEvent.click(within(dialog).getByRole("button", { name: "Schedule anonymisation" }));

    await vi.waitFor(() => expect(callsTo(fetchMock, "PUT", "/api/v1/customers/1005/anonymisation")).toHaveLength(1));
    const [, init] = callsTo(fetchMock, "PUT", "/api/v1/customers/1005/anonymisation")[0];
    expect(JSON.parse(String(init?.body))).toEqual({ anonymiseOn: day });
    // The answer is the customer as it now is, so the banner is there without
    // waiting for a refetch.
    expect(await screen.findByText(/^Anonymisation scheduled for /)).toBeInTheDocument();
  });

  it("asks for a day, there being no default, before asking the server", async () => {
    const fetchMock = await renderHeader(person({ status: "archived" }), { canManagePersonalData: true });
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" }));
    const dialog = await screen.findByRole("dialog", { name: "Schedule anonymisation" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Schedule anonymisation" }));
    expect(await within(dialog).findByText("Choose a day")).toBeInTheDocument();
    expect(callsTo(fetchMock, "PUT", "/api/v1/customers/1005/anonymisation")).toHaveLength(0);
  });

  it("says in words why the server refused a schedule", async () => {
    await renderHeader(person({ status: "archived" }), { canManagePersonalData: true }, (url, init) =>
      url === "/api/v1/customers/1005/anonymisation" && init?.method === "PUT"
        ? json(409, {
            title: "Customer is not archived",
            status: 409,
            code: "personal_data_customer_active",
            detail: "#5 Kari Nordmann is active.",
          })
        : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" }));
    const dialog = await screen.findByRole("dialog", { name: "Schedule anonymisation" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /anonymise on/i }), utcDay(40));
    await userEvent.click(within(dialog).getByRole("button", { name: "Schedule anonymisation" }));
    expect(
      await within(dialog).findByText(
        "This customer is not archived any more. Archive it before scheduling its anonymisation.",
      ),
    ).toBeInTheDocument();
  });

  it("shows a scheduled day in a banner, and calls the schedule off through the confirm modal", async () => {
    const fetchMock = await renderHeader(
      person({ status: "archived", anonymisation: { anonymiseOn: "2099-01-31" } }),
      { canManagePersonalData: true },
      (url, init) =>
        url === "/api/v1/customers/1005/anonymisation" && init?.method === "DELETE"
          ? json(200, person({ status: "archived", revision: 5 }))
          : undefined,
    );
    expect(screen.getByText(/^Anonymisation scheduled for /)).toBeInTheDocument();
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Cancel anonymisation" }));
    const confirm = await screen.findByRole("dialog", { name: "Cancel the anonymisation?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel anonymisation" }));
    await vi.waitFor(() =>
      expect(callsTo(fetchMock, "DELETE", "/api/v1/customers/1005/anonymisation")).toHaveLength(1),
    );
  });

  it("shows an anonymised customer's banner and none of the actions that would edit it", async () => {
    await renderHeader(
      person({
        name: "Anonymised person",
        status: "archived",
        anonymisation: { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" },
      }),
      { canManagePersonalData: true, canRestore: true, canMerge: true },
    );
    expect(screen.getByText(/^Anonymised on /)).toBeInTheDocument();
    expect(screen.queryByText(/this customer is archived/i)).not.toBeInTheDocument();
    for (const action of ["Edit customer", "Change type", "Restore customer", "Merge…"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Export personal data" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /anonymis/i })).not.toBeInTheDocument();
  });
});

/**
 * The page body under a router with every capability the host could pass, each
 * card answered with the empty body the server sends — the merge-header test's
 * `renderOverview`, for a private person.
 */
const renderOverview = async (body: ReturnType<typeof person>) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1005") return Promise.resolve(json(200, body));
      if (path === "/api/v1/customers/1005/addresses") return Promise.resolve(json(200, { data: [] }));
      if (path.startsWith("/api/v1/customers/1005/contacts")) return Promise.resolve(json(200, { data: [] }));
      if (path.startsWith("/api/v1/customers/1005/timeline")) return Promise.resolve(json(200, { data: [] }));
      if (path === "/api/v1/customers/1005/billing-profile")
        return Promise.resolve(json(200, { revision: 4, warnings: [] }));
      if (path === "/api/v1/customers/tags" || path === "/api/v1/customers/groups")
        return Promise.resolve(json(200, []));
      return Promise.resolve(new Response(null, { status: 404 }));
    }),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerOverview
                customerId={1005}
                canEdit
                canManageBilling
                canViewIdentity
                canManageIdentity
                canManageTimeline
              />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
  const settled = { timeout: 5_000 };
  await screen.findByText("No addresses yet", undefined, settled);
  await screen.findByText("No contacts associated with this customer yet.", undefined, settled);
  await screen.findByText("No events yet. Add the first moment worth remembering.", undefined, settled);
  await screen.findAllByText("Not set — the invoicing default applies", undefined, settled);
};

describe("customer page body — an anonymised customer", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("offers no edit action on any card, while a scheduled one still does", async () => {
    const cardButtons = ["Add contact", "Add event", "Add address", "Edit contact details", "Edit billing profile"];
    // A scheduled customer is not anonymised yet: its cards stay editable, so
    // their absence below is the anonymisation's gate.
    await renderOverview(person({ status: "archived", anonymisation: { anonymiseOn: "2099-01-31" } }));
    for (const action of cardButtons) expect(screen.getByRole("button", { name: action })).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderOverview(
      person({
        name: "Anonymised person",
        status: "archived",
        anonymisation: { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" },
      }),
    );
    for (const action of cardButtons) expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
  });
});
