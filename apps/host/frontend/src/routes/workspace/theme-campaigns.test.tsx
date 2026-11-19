import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { computeTokens } from "@vantigo/frontend-shell";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../../test/fetch";
import { renderRoute } from "../../test/render-route";
import { Route } from "./theme";
import "../../i18n";

const VANTIGO = "22222222-2222-4222-8222-222222222222";
const JUL = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const PRIDE = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const OLD = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const refusal = (status: number, code: string, message = `server text for ${code}`) =>
  json({ error: { code, message } }, status);

const campaign = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  brandId: VANTIGO,
  name,
  startsOn: "2026-12-01",
  endsOn: "2027-01-06",
  yearly: true,
  enabled: true,
  affectsEmail: false,
  revision: 3,
  status: "upcoming",
  overlay: { primary: "#b91c1c", logos: {} },
  ...over,
});

interface Call {
  method: string;
  url: string;
  body?: unknown;
}

let calls: Call[];
let campaigns: unknown[];
let respond: (c: Call) => Response | undefined;

const mount = async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const call: Call = { method, url, body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined };
    calls.push(call);
    const custom = respond(call);
    if (custom) return Promise.resolve(custom);
    if (method === "GET" && url === "/api/v1/identity/brands") {
      return Promise.resolve(
        json({
          items: [
            {
              id: VANTIGO,
              name: "Vantigo",
              title: "Vantigo",
              primary: "#4f46e5",
              isDefault: true,
              state: "published",
              hasDraft: false,
              publishedVersion: 2,
              revision: 5,
            },
            {
              id: OLD,
              name: "Old brand",
              title: "Old brand",
              primary: "#4f46e5",
              isDefault: false,
              state: "archived",
              hasDraft: false,
              publishedVersion: 2,
              revision: 5,
            },
          ],
        }),
      );
    }
    if (method === "GET" && url === "/api/v1/identity/brand-rules") {
      return Promise.resolve(json({ revision: 1, staffEnabled: false, items: [] }));
    }
    if (method === "GET" && url === `/api/v1/identity/brands/${VANTIGO}`) {
      return Promise.resolve(
        json({
          id: VANTIGO,
          isDefault: true,
          state: "published",
          revision: 5,
          publishedVersion: 2,
          published: {
            name: "Vantigo",
            title: "Vantigo",
            primary: "#4f46e5",
            radius: "md",
            support: {},
            logos: {},
            algorithm: 1,
            tokens: JSON.parse(JSON.stringify(computeTokens("#4f46e5", "md"))),
          },
          draft: null,
          checks: [],
          logoStorageAvailable: true,
        }),
      );
    }
    if (method === "GET" && url === `/api/v1/identity/brands/${VANTIGO}/campaigns`) {
      return Promise.resolve(json({ today: "2026-11-19", items: campaigns }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/theme", Route.options.component as never, queryClient);
  await screen.findByTestId("brand-list");
  fireEvent.click(screen.getByRole("tab", { name: "Campaigns" }));
  await screen.findByRole("heading", { name: "Campaigns" });
};

beforeEach(() => {
  window.sessionStorage.clear();
  calls = [];
  respond = () => undefined;
  campaigns = [
    campaign(JUL, "Jul"),
    campaign(PRIDE, "Pride", {
      startsOn: "2026-06-01",
      endsOn: "2026-06-30",
      yearly: false,
      status: "ended",
      overlay: { primary: "#7c3aed", logos: {} },
    }),
  ];
});

describe("the campaigns tab", () => {
  it("lists a brand's campaigns with their dates and where each stands, and offers no archived brand", async () => {
    await mount();
    const list = await screen.findByTestId("campaign-list");
    const jul = within(within(list).getByText("Jul").closest("tr") as HTMLElement);
    expect(jul.getByText("Upcoming")).toBeInTheDocument();
    expect(jul.getByText(/(1 Dec|Dec 1).* to .*(6 Jan|Jan 6), every year/)).toBeInTheDocument();
    const pride = within(within(list).getByText("Pride").closest("tr") as HTMLElement);
    expect(pride.getByText("Ended")).toBeInTheDocument();
    expect(pride.getByText(/(1 Jun|Jun 1),? 2026 to (30 Jun|Jun 30),? 2026/)).toBeInTheDocument();
    expect(calls.some((c) => c.url === `/api/v1/identity/brands/${VANTIGO}/campaigns`)).toBe(true);
    expect(screen.queryByText("Old brand")).toBeNull();
  });

  it("shows an active campaign as active now", async () => {
    campaigns = [campaign(JUL, "Jul", { status: "active" })];
    await mount();
    expect(await screen.findByText("Active now")).toBeInTheDocument();
  });

  it("edits a campaign and saves its inputs, with the preview in both colour schemes", async () => {
    respond = (c) =>
      c.method === "PUT" && c.url.endsWith(`/campaigns/${JUL}`)
        ? json(campaign(JUL, "Jul 2026", { revision: 4 }))
        : undefined;
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Jul").closest("tr") as HTMLElement).getByRole("button", { name: "Edit" }),
    );
    const editor = await screen.findByTestId("campaign-editor");
    expect(within(editor).getByText(/There is no draft/)).toBeInTheDocument();
    expect(within(editor).getByTestId("brand-preview-light")).toBeInTheDocument();
    expect(within(editor).getByTestId("brand-preview-dark")).toBeInTheDocument();

    fireEvent.change(within(editor).getByLabelText("Name"), { target: { value: "Jul 2026" } });
    fireEvent.change(within(editor).getByLabelText("Title"), { target: { value: "God jul" } });
    fireEvent.click(within(editor).getByRole("button", { name: "Save and go live" }));

    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const put = calls.find((c) => c.method === "PUT") as Call;
    expect(put.url).toBe(`/api/v1/identity/brands/${VANTIGO}/campaigns/${JUL}`);
    expect(put.body).toEqual({
      name: "Jul 2026",
      startsOn: "2026-12-01",
      endsOn: "2027-01-06",
      yearly: true,
      enabled: true,
      affectsEmail: false,
      overlay: { primary: "#b91c1c", title: "God jul" },
    });
    expect(await screen.findByText("The campaign was saved")).toBeInTheDocument();
  });

  it("sends tokens never: the body is the inputs and nothing the client computed", async () => {
    respond = (c) => (c.method === "PUT" ? json(campaign(JUL, "Jul")) : undefined);
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Jul").closest("tr") as HTMLElement).getByRole("button", { name: "Edit" }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Save and go live" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const body = (calls.find((c) => c.method === "PUT") as Call).body as Record<string, unknown>;
    expect(Object.keys(body).sort()).toEqual([
      "affectsEmail",
      "enabled",
      "endsOn",
      "name",
      "overlay",
      "startsOn",
      "yearly",
    ]);
    expect(Object.keys(body.overlay as object)).toEqual(["primary"]);
  });

  it("names the clash when the server refuses a campaign that overlaps another", async () => {
    respond = (c) =>
      c.method === "PUT"
        ? refusal(409, "campaign_overlap", 'The campaign "Pride" is active on 2026-12-03 as well.')
        : undefined;
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Jul").closest("tr") as HTMLElement).getByRole("button", { name: "Edit" }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Save and go live" }));
    expect(await screen.findByText(/The campaign "Pride" is active on 2026-12-03 as well/)).toBeInTheDocument();
    // The form stays open with what was typed.
    expect(screen.getByTestId("campaign-editor")).toBeInTheDocument();
  });

  it("holds a yearly range of more than a year back before it is sent", async () => {
    campaigns = [
      campaign(PRIDE, "Long", { startsOn: "2026-01-01", endsOn: "2027-01-01", yearly: false, status: "upcoming" }),
    ];
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Long").closest("tr") as HTMLElement).getByRole("button", { name: "Edit" }),
    );
    const editor = await screen.findByTestId("campaign-editor");
    expect(within(editor).getByRole("button", { name: "Save and go live" })).toBeEnabled();
    fireEvent.click(within(editor).getByRole("switch", { name: /Repeat every year/ }));
    expect(
      await within(editor).findByText("A campaign that repeats every year can last at most 365 days."),
    ).toBeInTheDocument();
    expect(within(editor).getByRole("button", { name: "Save and go live" })).toBeDisabled();
  });

  it("will not save a new campaign before it has dates", async () => {
    await mount();
    fireEvent.click(await screen.findByRole("button", { name: "New campaign" }));
    const editor = await screen.findByTestId("campaign-editor");
    fireEvent.change(within(editor).getByLabelText("Name"), { target: { value: "Easter" } });
    expect(within(editor).getByRole("button", { name: "Save and go live" })).toBeDisabled();
  });

  it("refuses a colour that cannot make a scale", async () => {
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Jul").closest("tr") as HTMLElement).getByRole("button", { name: "Edit" }),
    );
    const editor = await screen.findByTestId("campaign-editor");
    fireEvent.change(within(editor).getByLabelText(/Primary colour/), { target: { value: "#ffffff" } });
    expect(await within(editor).findByText(/too light or too dark/)).toBeInTheDocument();
    expect(within(editor).getByRole("button", { name: "Save and go live" })).toBeDisabled();
  });

  it("deletes a campaign after a confirmation", async () => {
    respond = (c) => (c.method === "DELETE" ? new Response(null, { status: 204 }) : undefined);
    await mount();
    const list = await screen.findByTestId("campaign-list");
    fireEvent.click(
      within(within(list).getByText("Jul").closest("tr") as HTMLElement).getByRole("button", { name: "Delete" }),
    );
    expect(await screen.findByText("Delete Jul?")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect((calls.find((c) => c.method === "DELETE") as Call).url).toBe(
      `/api/v1/identity/brands/${VANTIGO}/campaigns/${JUL}`,
    );
    expect(await screen.findByText("The campaign was deleted")).toBeInTheDocument();
  });
});
