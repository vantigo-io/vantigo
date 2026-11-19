import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { computeTokens } from "@vantigo/frontend-shell";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../../test/fetch";
import { renderRoute } from "../../test/render-route";
import { insertIndexFor, moveItem } from "./-brand-rules-order";
import { Route } from "./theme";
import "../../i18n";

const NORDLYS = "11111111-1111-4111-8111-111111111111";
const VANTIGO = "22222222-2222-4222-8222-222222222222";
const OLD = "33333333-3333-4333-8333-333333333333";
const DRAFT = "44444444-4444-4444-8444-444444444444";
const GROUP = "55555555-5555-4555-8555-555555555555";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const refusal = (status: number, code: string) => json({ error: { code, message: `server text for ${code}` } }, status);

const summary = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  name,
  title: name,
  primary: "#4f46e5",
  isDefault: false,
  state: "published",
  hasDraft: false,
  publishedVersion: 2,
  revision: 5,
  ...over,
});

const fullBrand = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  isDefault: false,
  state: "published",
  revision: 5,
  publishedVersion: 2,
  published: {
    name,
    title: name,
    primary: "#0f766e",
    radius: "md",
    support: { email: "help@acme.test" },
    logos: {},
    algorithm: 1,
    tokens: JSON.parse(JSON.stringify(computeTokens("#0f766e", "md"))),
  },
  draft: null,
  checks: [],
  logoStorageAvailable: true,
  ...over,
});

const rule = (
  id: string,
  brandId: string,
  brandName: string,
  kind: string,
  targetRef: string,
  label: string,
  over = {},
) => ({
  id,
  brandId,
  brandName,
  position: 0,
  kind,
  targetRef,
  targetLabel: label,
  stale: false,
  ...over,
});

interface Calls {
  method: string;
  url: string;
  body?: unknown;
  ifMatch?: string | null;
}

let brands: unknown[];
let rules: { revision: number; staffEnabled: boolean; items: unknown[] };
let calls: Calls[];
let respond: (c: Calls) => Response | undefined;

const mount = async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    const call: Calls = {
      method,
      url,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
      ifMatch: headers.get("If-Match"),
    };
    calls.push(call);
    const custom = respond(call);
    if (custom) return Promise.resolve(custom);
    if (method === "GET" && url === "/api/v1/identity/brands") return Promise.resolve(json({ items: brands }));
    if (method === "GET" && url === "/api/v1/identity/brand-rules") return Promise.resolve(json(rules));
    if (method === "GET" && url === "/api/v1/identity/brand-rule-targets") {
      return Promise.resolve(
        json({
          staffEnabled: rules.staffEnabled,
          accessGroups: [{ id: GROUP, name: "Oslo office", available: true }],
          legalEntities: rules.staffEnabled ? [{ id: "1001", name: "Nordlys AS", available: true }] : [],
        }),
      );
    }
    const one = /^\/api\/v1\/identity\/brands\/([0-9a-f-]+)$/.exec(url);
    if (method === "GET" && one) {
      const s = (brands as { id: string; name: string }[]).find((b) => b.id === one[1]);
      return Promise.resolve(s ? json(fullBrand(s.id, s.name)) : new Response(null, { status: 404 }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/theme", Route.options.component as never, queryClient);
  await screen.findByTestId("brand-list");
};

const rowOf = (name: string) => screen.getByRole("button", { name }).closest("tr") as HTMLElement;
const menuOf = async (name: string) => {
  fireEvent.click(screen.getByRole("button", { name: `Actions for ${name}` }));
  return screen.findByRole("menu");
};
const openRules = async () => {
  fireEvent.click(screen.getByRole("tab", { name: "Assignment" }));
  await screen.findByText("Who sees which brand");
};
const ruleTargets = () => screen.getAllByTestId("brand-rule").map((r) => (r as HTMLElement).dataset.target);

beforeEach(() => {
  window.sessionStorage.clear();
  calls = [];
  respond = () => undefined;
  brands = [
    summary(VANTIGO, "Vantigo", { isDefault: true }),
    summary(NORDLYS, "Nordlys"),
    summary(OLD, "Old brand", { state: "archived" }),
    summary(DRAFT, "Pending", { state: "draft", publishedVersion: 0, revision: 2 }),
  ];
  rules = {
    revision: 7,
    staffEnabled: true,
    items: [
      rule("r1", NORDLYS, "Nordlys", "domain", "nordlys.example", "nordlys.example"),
      rule("r2", NORDLYS, "Nordlys", "access_group", GROUP, "Oslo office"),
      rule("r3", VANTIGO, "Vantigo", "legal_entity", "1001", "Nordlys AS"),
    ],
  };
});

describe("the ordering helpers", () => {
  it("moves an item and leaves the list alone for a move that is none", () => {
    expect(moveItem(["a", "b", "c", "d"], 3, 1)).toEqual(["a", "d", "b", "c"]);
    expect(moveItem(["a", "b", "c"], 0, 2)).toEqual(["b", "c", "a"]);
    expect(moveItem(["a", "b"], 1, 1)).toEqual(["a", "b"]);
    expect(moveItem(["a", "b"], 0, 5)).toEqual(["a", "b"]);
  });

  it("puts a new rule after the last of its kind or of an earlier one: domains, entities, then groups", () => {
    const list = [{ kind: "domain" as const }, { kind: "access_group" as const }, { kind: "legal_entity" as const }];
    expect(insertIndexFor(list, "domain")).toBe(1);
    expect(insertIndexFor(list, "legal_entity")).toBe(3);
    expect(insertIndexFor(list, "access_group")).toBe(3);
    expect(insertIndexFor([], "access_group")).toBe(0);
  });
});

describe("the brands list", () => {
  it("lists every brand with its state, the default marked and the number of rules", async () => {
    await mount();
    const nordlys = within(rowOf("Nordlys"));
    expect(nordlys.getByText("Published")).toBeInTheDocument();
    expect(nordlys.getByText("2")).toBeInTheDocument();
    const vantigo = within(rowOf("Vantigo"));
    expect(vantigo.getByText("Default")).toBeInTheDocument();
    expect(vantigo.getByText("1")).toBeInTheDocument();
    expect(within(rowOf("Old brand")).getByText("Archived")).toBeInTheDocument();
    expect(within(rowOf("Pending")).getByText("Never published")).toBeInTheDocument();
  });

  it("offers each brand only the actions that apply to it", async () => {
    await mount();
    const actions = async (name: string) =>
      within(await menuOf(name))
        .getAllByRole("menuitem")
        .map((m) => m.textContent);
    expect(await actions("Vantigo")).toEqual(["Edit", "Duplicate", "Export as file"]);
    fireEvent.keyDown(document.body, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(await actions("Nordlys")).toEqual(["Edit", "Duplicate", "Make default", "Export as file", "Archive"]);
    fireEvent.keyDown(document.body, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(await actions("Old brand")).toEqual(["Export as file", "Restore"]);
    fireEvent.keyDown(document.body, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(await actions("Pending")).toEqual(["Edit", "Duplicate", "Export as file", "Delete"]);
  });

  it("duplicates at the revision it was read at", async () => {
    respond = (c) =>
      c.method === "POST" && c.url.endsWith("/duplicate") ? json(fullBrand(DRAFT, "Nordlys (copy)"), 201) : undefined;
    await mount();
    fireEvent.click(within(await menuOf("Nordlys")).getByRole("menuitem", { name: "Duplicate" }));
    await waitFor(() => expect(calls.some((c) => c.url === `/api/v1/identity/brands/${NORDLYS}/duplicate`)).toBe(true));
    const call = calls.find((c) => c.url.endsWith("/duplicate"));
    expect(call).toMatchObject({ method: "POST", ifMatch: "5" });
    expect(await screen.findByText("Brand duplicated")).toBeInTheDocument();
  });

  it("asks before it archives, makes default or deletes, and then does it", async () => {
    respond = (c) => {
      if (c.method === "POST" && /\/(archive|make-default)$/.test(c.url)) return json(fullBrand(NORDLYS, "Nordlys"));
      if (c.method === "DELETE") return new Response(null, { status: 204 });
      return undefined;
    };
    await mount();
    fireEvent.click(within(await menuOf("Nordlys")).getByRole("menuitem", { name: "Archive" }));
    expect(await screen.findByText("Archive Nordlys?")).toBeInTheDocument();
    expect(calls.some((c) => c.url.endsWith("/archive"))).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(calls.some((c) => c.url === `/api/v1/identity/brands/${NORDLYS}/archive`)).toBe(true));
    expect(await screen.findByText("Brand archived")).toBeInTheDocument();
  });

  it("deletes a brand that was never published, and says when the server refuses", async () => {
    respond = (c) => (c.method === "DELETE" ? refusal(409, "brand_in_use") : undefined);
    await mount();
    fireEvent.click(within(await menuOf("Pending")).getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.find((c) => c.method === "DELETE")).toMatchObject({ ifMatch: "2" }));
    expect(await screen.findByText(/cannot be archived, and a brand that has been published/)).toBeInTheDocument();
  });

  it("restores an archived brand, and shows the notice instead of an editor for it", async () => {
    respond = (c) =>
      c.method === "POST" && c.url.endsWith("/restore") ? json(fullBrand(OLD, "Old brand")) : undefined;
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Old brand" }));
    expect(await screen.findByText(/This brand is archived/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/primary colour/i)).toBeNull();
    fireEvent.click(within(await menuOf("Old brand")).getByRole("menuitem", { name: "Restore" }));
    await waitFor(() => expect(calls.some((c) => c.url === `/api/v1/identity/brands/${OLD}/restore`)).toBe(true));
  });

  it("creates a brand from a name and a colour, and says when the name is taken", async () => {
    respond = (c) =>
      c.method === "POST" && c.url === "/api/v1/identity/brands" ? refusal(409, "brand_name_taken") : undefined;
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "New brand" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: "Fjord" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")).toBeTruthy());
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ name: "Fjord", title: "Fjord", radius: "md" });
    expect((await within(dialog).findAllByText("Another brand already has that name.")).length).toBeGreaterThan(0);
  });
});

describe("the rule list", () => {
  it("shows the rules in order with their kind, target and brand, and flags a stale one", async () => {
    rules.items[1] = rule("r2", NORDLYS, "Nordlys", "access_group", GROUP, "Oslo office", {
      stale: true,
      staleReason: "inactive",
    });
    await mount();
    await openRules();
    expect(ruleTargets()).toEqual(["nordlys.example", GROUP, "1001"]);
    const second = screen.getAllByTestId("brand-rule")[1] as HTMLElement;
    expect(within(second).getByText("Access group")).toBeInTheDocument();
    expect(within(second).getByRole("note")).toHaveTextContent("The group is inactive; the rule matches no one.");
    expect(screen.getByRole("button", { name: "Save rules" })).toBeDisabled();
  });

  it("reorders with the arrows and saves the whole list at its revision", async () => {
    respond = (c) => (c.method === "PUT" ? json({ ...rules, revision: 8 }) : undefined);
    await mount();
    await openRules();
    fireEvent.click(screen.getByRole("button", { name: "Move Nordlys AS up" }));
    fireEvent.click(screen.getByRole("button", { name: "Move Nordlys AS up" }));
    expect(ruleTargets()).toEqual(["1001", "nordlys.example", GROUP]);
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")).toBeTruthy());
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.ifMatch).toBe("7");
    expect(put?.body).toEqual({
      items: [
        { brandId: VANTIGO, kind: "legal_entity", targetRef: "1001" },
        { brandId: NORDLYS, kind: "domain", targetRef: "nordlys.example" },
        { brandId: NORDLYS, kind: "access_group", targetRef: GROUP },
      ],
    });
    expect(await screen.findByText("Rules saved")).toBeInTheDocument();
  });

  it("reorders by dragging a rule onto another", async () => {
    await mount();
    await openRules();
    const [first, , third] = screen.getAllByTestId("brand-rule") as HTMLElement[];
    const data: Record<string, string> = {};
    const dataTransfer = { setData: (k: string, v: string) => (data[k] = v), getData: (k: string) => data[k] };
    fireEvent.dragStart(third as HTMLElement, { dataTransfer });
    fireEvent.dragOver(first as HTMLElement, { dataTransfer });
    fireEvent.drop(first as HTMLElement, { dataTransfer });
    expect(ruleTargets()).toEqual(["1001", "nordlys.example", GROUP]);
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(ruleTargets()).toEqual(["nordlys.example", GROUP, "1001"]);
  });

  it("adds a rule where its kind belongs, removes one, and changes the brand of one", async () => {
    await mount();
    await openRules();
    fireEvent.change(screen.getByLabelText("Target"), { target: { value: "acme.no" } });
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    expect(ruleTargets()).toEqual(["nordlys.example", "acme.no", GROUP, "1001"]);
    fireEvent.click(screen.getByRole("button", { name: "Remove the rule for Oslo office" }));
    expect(ruleTargets()).toEqual(["nordlys.example", "acme.no", "1001"]);
    expect(screen.getByRole("button", { name: "Save rules" })).toBeEnabled();
  });

  it("offers legal entities only while Staff is enabled", async () => {
    rules = { revision: 1, staffEnabled: false, items: [] };
    await mount();
    await openRules();
    expect(screen.getByText("No rules yet: everyone sees the default brand.")).toBeInTheDocument();
    fireEvent.click(screen.getAllByLabelText("Kind of rule")[0] as HTMLElement);
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["E-mail domain", "Access group"]);
  });

  it("explains a refusal in words", async () => {
    respond = (c) => (c.method === "PUT" ? refusal(422, "domain_taken") : undefined);
    await mount();
    await openRules();
    fireEvent.click(screen.getByRole("button", { name: "Move Oslo office up" }));
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That domain already belongs to another brand.");
  });
});

describe("who sees what", () => {
  const lookup = async (email = "ola@nordlys.example") => {
    await mount();
    await openRules();
    fireEvent.change(screen.getByLabelText("Person's e-mail address"), { target: { value: email } });
    fireEvent.click(screen.getByRole("button", { name: "Look up" }));
  };

  it("shows the brand a person gets and the rule that decided, and no employment detail the server withheld", async () => {
    respond = (c) =>
      c.url.startsWith("/api/v1/identity/brands/resolution?email=ola%40nordlys.example")
        ? json({
            userId: GROUP,
            displayName: "Ola Nordmann",
            brand: { id: NORDLYS, name: "Nordlys" },
            source: "rule",
            rule: {
              id: "r1",
              kind: "domain",
              targetRef: "nordlys.example",
              targetLabel: "nordlys.example",
              position: 1,
            },
            candidates: [
              { id: NORDLYS, name: "Nordlys" },
              { id: VANTIGO, name: "Vantigo" },
            ],
            choiceIgnored: false,
          })
        : undefined;
    await lookup();
    const result = await screen.findByTestId("brand-lookup-result");
    expect(result).toHaveTextContent("Ola Nordmann sees Nordlys.");
    expect(result).toHaveTextContent("Rule 1 decides: E-mail domain nordlys.example.");
    expect(result).toHaveTextContent("Brands they may choose among: Nordlys, Vantigo.");
    expect(screen.queryByTestId("brand-lookup-details")).toBeNull();
  });

  it("shows the fuller explanation only when the server sent it", async () => {
    respond = (c) =>
      c.url.includes("/brands/resolution")
        ? json({
            userId: GROUP,
            displayName: "Ola Nordmann",
            brand: { id: VANTIGO, name: "Vantigo" },
            source: "default",
            candidates: [{ id: VANTIGO, name: "Vantigo" }],
            choiceIgnored: false,
            details: { emailDomain: "x.example", accessGroups: ["Oslo office"], legalEntities: [] },
          })
        : undefined;
    await lookup();
    expect(await screen.findByText("No rule matches them, so they get the default brand.")).toBeInTheDocument();
    const details = screen.getByTestId("brand-lookup-details");
    expect(details).toHaveTextContent("E-mail domain: x.example");
    expect(details).toHaveTextContent("Access groups: Oslo office");
    expect(details).toHaveTextContent("Legal entities: none");
  });

  it("says when nobody has the address", async () => {
    respond = (c) => (c.url.includes("/brands/resolution") ? new Response(null, { status: 404 }) : undefined);
    await lookup("nobody@x.example");
    expect(await screen.findByText("Nobody has that address.")).toBeInTheDocument();
  });
});
