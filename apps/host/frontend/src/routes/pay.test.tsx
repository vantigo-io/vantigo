import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider, setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import indexHtml from "../../index.html?raw";
import { AccountLanguagePreference } from "../components/account-language-preference";
import { publicPaths, sessionlessPaths } from "../lib/public-paths";
import { routeTree } from "../routeTree.gen";

// A token with characters a URL encodes, so the encoded check below means something.
const TOKEN = "tok-a+b/c=_Z";

interface AllowedRoute {
  methods: string[];
  path: string;
  prefix: boolean;
  oneSegment: boolean;
}

/**
 * The public host's allowlist, read from the server's own source
 * (`security.PayRoutes()`), so this test and the gate cannot drift apart.
 */
const payRoutes = (): AllowedRoute[] => {
  const source = Object.values(
    import.meta.glob<string>("../../../../server/internal/security/publichost.go", {
      query: "?raw",
      import: "default",
      eager: true,
    }),
  )[0];
  if (!source) throw new Error("publichost.go was not found");
  const body = source.slice(source.indexOf("func PayRoutes()"));
  const block = body.slice(0, body.indexOf("\n}\n"));
  const methods: Record<string, string[]> = { getHead: ["GET", "HEAD"], get: ["GET"], post: ["POST"] };
  const routes = [...block.matchAll(/\{Methods: (\w+), Path: "([^"]+)"([^}]*)\}/g)].map(([, m, p, rest]) => ({
    methods: methods[m] ?? [],
    path: p,
    prefix: rest.includes("Prefix: true"),
    oneSegment: rest.includes("OneSegment: true"),
  }));
  if (routes.length === 0) throw new Error("PayRoutes() was not found in publichost.go");
  return routes;
};

/** Whether the gate admits a request — `Route.matches` in publichost.go, on the path alone (any query). */
const admitted = (routes: AllowedRoute[], method: string, url: string) => {
  const { pathname } = new URL(url, "https://pay.example.com");
  return routes.some((route) => {
    if (!route.methods.includes(method)) return false;
    if (route.prefix) return pathname.startsWith(route.path);
    if (route.oneSegment) {
      const rest = pathname.startsWith(route.path) ? pathname.slice(route.path.length) : undefined;
      return rest !== undefined && rest !== "" && !rest.includes("/");
    }
    return pathname === route.path;
  });
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** What the RSVP page's read answers. */
const rsvpPage = (status = "needs-action") => ({
  title: "Kickoff with Nordlys AS",
  allDay: false,
  startsAt: "2026-11-03T08:00:00Z",
  endsAt: "2026-11-03T09:00:00Z",
  timeZone: "Europe/Oslo",
  organiser: { name: "Kari Nordmann" },
  attendee: { name: "Per Utenfor" },
  status,
  cancelled: false,
  language: "en",
});

/** Every request the SPA makes, recorded; the pay and RSVP pages' operations and the maintenance poll answered. */
const recordFetch = () => {
  const requests: { url: string; method: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      requests.push({ url, method, init });
      if (url === "/api/v1/identity/system/status") return Promise.resolve(json({ maintenance: false, message: null }));
      if (url === "/api/v1/invoices/pay/read" && method === "POST")
        return Promise.resolve(
          json({
            seller: { name: "Vantigo Demo AS", organisationNumber: "999888777" },
            invoiceNumber: 10042,
            issueDate: "2026-09-01",
            dueDate: "2026-09-15",
            openAmount: 1250,
            currency: "NOK",
            kid: "0001004207",
            termsUrl: "https://seller.example.com/terms",
            privacyUrl: "https://seller.example.com/privacy",
            language: "en",
            payable: true,
          }),
        );
      if (url === "/api/v1/calendar/rsvp/read" && method === "POST") return Promise.resolve(json(rsvpPage()));
      if (url === "/api/v1/calendar/rsvp" && method === "POST")
        return Promise.resolve(json(rsvpPage(JSON.parse(String(init?.body)).status)));
      // What the public host answers anything else: a 404.
      return Promise.resolve(new Response(null, { status: 404 }));
    }),
  );
  return requests;
};

/** The SPA as main.tsx mounts it — the providers above the router, the account language among them — at `address`. */
const renderSpaAt = (address: string) => {
  window.history.replaceState(null, "", address);
  const queryClient = new QueryClient();
  const router = createRouter({
    routeTree,
    context: { queryClient },
    // The browser's own history, so the router and the address bar are one.
    history: createBrowserHistory(),
  });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <AccountLanguagePreference />
          <RouterProvider router={router} />
        </I18nProvider>
      </QueryClientProvider>
    </MantineProvider>,
  );
  return router;
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
  window.history.replaceState(null, "", "/");
  window.sessionStorage.clear();
  setLanguagePreference("auto");
});

describe("the /pay route", () => {
  it("renders without a session and asks for nothing outside the public host's allowlist", async () => {
    const requests = recordFetch();
    const router = renderSpaAt(`/pay?token=${encodeURIComponent(TOKEN)}`);

    expect(await screen.findByText("Vantigo Demo AS")).toBeInTheDocument();
    // Let anything else the root or the page would ask for go out.
    await new Promise((resolve) => setTimeout(resolve, 50));

    const routes = payRoutes();
    const refused = requests.filter((request) => !admitted(routes, request.method, request.url));
    expect(refused.map((request) => `${request.method} ${request.url}`)).toEqual([]);
    const urls = requests.map((request) => request.url);
    expect(urls.some((url) => url.startsWith("/api/v1/identity/session"))).toBe(false);
    expect(urls.some((url) => url.startsWith("/api/v1/authorization"))).toBe(false);
    expect(urls.some((url) => url.includes("/profile"))).toBe(false);
    // The token is in no API URL: the read carries it in its body.
    expect(urls.some((url) => url.includes(TOKEN) || url.includes(encodeURIComponent(TOKEN)))).toBe(false);
    // On the public host the root does not even poll for maintenance.
    expect(urls).not.toContain("/api/v1/identity/system/status");
    const read = requests.find((request) => request.url === "/api/v1/invoices/pay/read");
    expect(JSON.parse(String(read?.init?.body))).toEqual({ token: TOKEN });
    // Same-origin requests only, so no credential travels anywhere else.
    expect(urls.every((url) => url.startsWith("/"))).toBe(true);
    // And the token is gone from the address bar, and from the router's own location.
    await vi.waitFor(() => expect(window.location.search).toBe(""));
    expect(window.location.pathname).toBe("/pay");
    expect(router.state.location.searchStr).toBe("");
  });
});

describe("the /rsvp route", () => {
  it("renders without a session, reads and answers, and asks for nothing outside the allowlist", async () => {
    const requests = recordFetch();
    const router = renderSpaAt(`/rsvp?token=${encodeURIComponent(TOKEN)}`);

    expect(await screen.findByRole("heading", { name: "Kickoff with Nordlys AS" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Accept" }));
    expect(await screen.findByText("Your answer: Accepted")).toBeInTheDocument();
    // Let anything else the root or the page would ask for go out.
    await new Promise((resolve) => setTimeout(resolve, 50));

    const routes = payRoutes();
    expect(admitted(routes, "GET", "/rsvp")).toBe(true);
    const refused = requests.filter((request) => !admitted(routes, request.method, request.url));
    expect(refused.map((request) => `${request.method} ${request.url}`)).toEqual([]);
    const urls = requests.map((request) => request.url);
    expect(urls.some((url) => url.startsWith("/api/v1/identity"))).toBe(false);
    expect(urls.some((url) => url.startsWith("/api/v1/authorization"))).toBe(false);
    expect(urls.some((url) => url.includes("/profile"))).toBe(false);
    // The token is in no API URL: the read and the answer carry it in their bodies.
    expect(urls.some((url) => url.includes(TOKEN) || url.includes(encodeURIComponent(TOKEN)))).toBe(false);
    const bodies = (url: string) =>
      requests.filter((request) => request.url === url).map((request) => JSON.parse(String(request.init?.body)));
    expect(bodies("/api/v1/calendar/rsvp/read")).toEqual([{ token: TOKEN }]);
    expect(bodies("/api/v1/calendar/rsvp")).toEqual([{ token: TOKEN, status: "accepted" }]);
    expect(urls.every((url) => url.startsWith("/"))).toBe(true);
    await vi.waitFor(() => expect(window.location.search).toBe(""));
    expect(window.location.pathname).toBe("/rsvp");
    expect(router.state.location.searchStr).toBe("");
  });
});

describe("publicPaths", () => {
  it("holds /rsvp exactly, and not the signed-in calendar", () => {
    expect(publicPaths.has("/rsvp")).toBe(true);
    expect(publicPaths.has("/rsvp/")).toBe(false);
    expect(publicPaths.has("/calendar")).toBe(false);
    expect(sessionlessPaths.has("/rsvp")).toBe(true);
    expect(sessionlessPaths.has("/rsvp/")).toBe(false);
  });

  it("holds /pay exactly, and not the signed-in Payments area", () => {
    expect(publicPaths.has("/pay")).toBe(true);
    expect(publicPaths.has("/pay/")).toBe(false);
    expect(publicPaths.has("/invoices/payments")).toBe(false);
    expect(publicPaths.has("/invoices/payments/vipps")).toBe(false);
  });
});

describe("the account's language on the public host", () => {
  const sessionAsked = async (address: string) => {
    const requests = recordFetch();
    window.history.replaceState(null, "", address);
    render(
      <QueryClientProvider client={new QueryClient()}>
        <AccountLanguagePreference />
      </QueryClientProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 50));
    return requests.some((request) => request.url.endsWith("/api/v1/identity/session"));
  };

  it("asks for no session on /pay under a base path", async () => {
    window.__VANTIGO_APP__ = { basePath: "/vantigo/" };
    expect(await sessionAsked("/vantigo/pay?token=x")).toBe(false);
  });

  it("asks for no session on /rsvp under a base path", async () => {
    window.__VANTIGO_APP__ = { basePath: "/vantigo/" };
    expect(await sessionAsked("/vantigo/rsvp?token=x")).toBe(false);
  });

  it("still asks for the session on a signed-in page under that base path", async () => {
    window.__VANTIGO_APP__ = { basePath: "/vantigo/" };
    expect(await sessionAsked("/vantigo/invoices")).toBe(true);
  });
});

describe("index.html", () => {
  it("sends no referrer, so no address the SPA is on leaks to another site", () => {
    expect(indexHtml).toContain('<meta name="referrer" content="no-referrer" />');
  });
});
