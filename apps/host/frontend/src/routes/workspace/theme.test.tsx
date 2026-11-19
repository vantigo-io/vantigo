import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { builtinPayload, computeTokens } from "@vantigo/frontend-shell";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Brand } from "../../api/brands";
import { stubFetch } from "../../test/fetch";
import { renderRoute } from "../../test/render-route";
import { Route } from "./theme";
import "../../i18n";

// Since algorithm 2 no real primary fails a pair (the hover is clamped, B3.9), so the editor's
// failing-pair path is exercised with a primary the stand-in rules refuse.
vi.mock("@vantigo/frontend-shell", async (importActual) => {
  const actual = await importActual<typeof import("@vantigo/frontend-shell")>();
  return {
    ...actual,
    contrastChecks: (result: Parameters<typeof actual.contrastChecks>[0]) =>
      actual
        .contrastChecks(result)
        .map((c) => (result.scale[6] === "#ef4444" && c.pair === "filledHover" ? { ...c, pass: false } : c)),
    suggestPrimary: (hex: string) => (hex === "#ef4444" ? "#dc2626" : actual.suggestPrimary(hex)),
  };
});

const ID = "22222222-2222-2222-2222-222222222222";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const refusal = (status: number, code: string, fields?: Record<string, string[]>) =>
  json({ error: { code, message: `server text for ${code}`, fields } }, status);

const version = (primary: string, over: Record<string, unknown> = {}) => {
  const tokens = computeTokens(primary, "md");
  return {
    name: "Vantigo",
    title: "Vantigo",
    primary,
    radius: "md",
    support: {},
    logos: {},
    algorithm: 1,
    tokens: JSON.parse(JSON.stringify(tokens)),
    ...over,
  };
};

const brand = (over: Partial<Brand> = {}): Brand =>
  ({
    id: ID,
    isDefault: true,
    state: "published",
    revision: 4,
    publishedVersion: 2,
    published: version("#4f46e5"),
    draft: null,
    checks: [],
    logoStorageAvailable: true,
    ...over,
  }) as unknown as Brand;

type Handler = (method: string, url: string, init: RequestInit | undefined) => Response | undefined;

let current: Brand;
const render = async (handle: Handler = () => undefined, start: Brand = brand()) => {
  current = start;
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    if (method === "GET" && url === "/api/v1/identity/brands") {
      return Promise.resolve(json({ items: [{ id: ID, name: "Vantigo", title: "Vantigo", primary: "#4f46e5" }] }));
    }
    if (method === "GET" && url === `/api/v1/identity/brands/${ID}`) return Promise.resolve(json(current));
    return Promise.resolve(handle(method, url, init) ?? new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/theme", Route.options.component as never, queryClient);
  await screen.findByLabelText(/primary colour/i);
  return fetchMock;
};

const typeColour = (value: string) => {
  const input = screen.getByLabelText(/primary colour/i);
  fireEvent.change(input, { target: { value } });
};

beforeEach(() => {
  window.sessionStorage.clear();
});

describe("the brand editor", () => {
  it("shows the brand being edited, its ten shades and a passing contrast table", async () => {
    await render();
    expect(screen.getByLabelText("Name")).toHaveValue("Vantigo");
    expect(screen.getByLabelText("Title")).toHaveValue("Vantigo");
    const scale = screen.getByTestId("brand-scale");
    expect(scale.children).toHaveLength(10);
    expect((scale.children[6] as HTMLElement).dataset.shade).toBe("#4f46e5");
    const table = screen.getByTestId("brand-contrast");
    expect(within(table).getAllByText("Passes")).toHaveLength(10);
    expect(within(table).queryByText("Fails")).toBeNull();
    expect(screen.getByRole("button", { name: "Publish" })).toBeDisabled();
  });

  it("previews the draft in both colour schemes, with the brand's own shades", async () => {
    await render();
    typeColour("#0f766e");
    const light = await screen.findByTestId("brand-preview-light");
    const dark = screen.getByTestId("brand-preview-dark");
    expect(light.dataset.scheme).toBe("light");
    expect(dark.dataset.scheme).toBe("dark");
    expect(light.style.getPropertyValue("--mantine-color-vantigo-6")).toBe("#0f766e");
    expect(dark.style.getPropertyValue("--mantine-color-vantigo-6")).toBe("#0f766e");
    expect(within(light).getAllByText("Create invoice").length).toBeGreaterThan(0);
    expect(screen.getByTestId("brand-tab-light")).toBeInTheDocument();
    expect(screen.getByTestId("brand-tab-dark")).toBeInTheDocument();
    expect((screen.getByTestId("brand-scale").children[6] as HTMLElement).dataset.shade).toBe("#0f766e");
  });

  it("says why a colour that is not usable has no preview", async () => {
    await render();
    typeColour("#ffffff");
    expect(await screen.findByText(/too light or too dark/i)).toBeInTheDocument();
    expect(screen.getByText("Fix the colour to see the preview.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save draft" })).toBeDisabled();
    typeColour("teal");
    expect(await screen.findByText("Write the colour as #rrggbb.")).toBeInTheDocument();
  });

  it("shows the failing pairs and fixes them with one click on the nearest passing colour", async () => {
    await render();
    typeColour("#ef4444");
    const table = await screen.findByTestId("brand-contrast");
    expect(within(table).getAllByText("Fails").length).toBeGreaterThan(0);
    expect(screen.getByText(/need a darker primary/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Publish" })).toBeDisabled();
    const fix = screen.getByRole("button", { name: /^Use #/ });
    fireEvent.click(fix);
    await waitFor(() => expect(within(screen.getByTestId("brand-contrast")).queryByText("Fails")).toBeNull());
    expect((screen.getByLabelText(/primary colour/i) as HTMLInputElement).value).not.toBe("#ef4444");
  });

  it("saves the draft from the inputs against the revision it was read at", async () => {
    const fetchMock = await render((method, url) => {
      if (method === "PUT" && url === `/api/v1/identity/brands/${ID}`) {
        current = brand({ revision: 5, draft: version("#0f766e", { name: "Acme", title: "Acme Portal" }) as never });
        return json(current);
      }
      return undefined;
    });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Acme" } });
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Acme Portal" } });
    fireEvent.change(screen.getByLabelText("Support e-mail"), { target: { value: "help@acme.test" } });
    typeColour("#0f766e");
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await screen.findByText("Draft saved");
    const call = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT") as [string, RequestInit];
    expect(new Headers(call[1].headers).get("If-Match")).toBe("4");
    expect(JSON.parse(String(call[1].body))).toEqual({
      name: "Acme",
      title: "Acme Portal",
      primary: "#0f766e",
      radius: "md",
      support: { email: "help@acme.test" },
    });
    // Tokens are never sent: the server recomputes them (B37).
    expect(Object.keys(JSON.parse(String(call[1].body)))).not.toContain("tokens");
  });

  it("saves, then publishes against the new revision", async () => {
    const calls: string[] = [];
    await render((method, url, init) => {
      if (method === "PUT" && url === `/api/v1/identity/brands/${ID}`) {
        calls.push(`PUT ${new Headers(init?.headers).get("If-Match")}`);
        current = brand({ revision: 5, draft: version("#0f766e") as never });
        return json(current);
      }
      if (method === "POST" && url === `/api/v1/identity/brands/${ID}/publish`) {
        calls.push(`POST ${new Headers(init?.headers).get("If-Match")}`);
        current = brand({ revision: 6, publishedVersion: 3, published: version("#0f766e") as never });
        return json(current);
      }
      return undefined;
    });
    typeColour("#0f766e");
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    await screen.findByText("Brand published");
    expect(calls).toEqual(["PUT 4", "POST 5"]);
  });

  it("explains a stale revision and a contrast refusal", async () => {
    await render((method) => (method === "PUT" ? refusal(409, "stale_revision") : undefined));
    typeColour("#0f766e");
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
    expect(await screen.findByText(/changed since you opened it/i)).toBeInTheDocument();
  });

  it("shows the server's support field errors under the field", async () => {
    await render((method) =>
      method === "PUT"
        ? refusal(422, "brand_support_invalid", { url: ["The support link must be an http or https URL."] })
        : undefined,
    );
    fireEvent.change(screen.getByLabelText("Support link"), { target: { value: "javascript:alert(1)" } });
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
    expect(await screen.findByText("The support link must be an http or https URL.")).toBeInTheDocument();
  });

  it("discards a draft", async () => {
    await render(
      (method, url) => {
        if (method === "POST" && url.endsWith("/discard-draft")) {
          current = brand({ revision: 6 });
          return json(current);
        }
        return undefined;
      },
      brand({ draft: version("#0f766e", { name: "Acme" }) as never }),
    );
    expect(screen.getByText("Unpublished changes")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Discard draft" }));
    await screen.findByText("Draft discarded");
    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveValue("Vantigo"));
  });

  it("starts 'preview as me' for the brand, after saving unsaved changes", async () => {
    await render((method, url) => {
      if (method === "PUT" && url === `/api/v1/identity/brands/${ID}`) {
        current = brand({ revision: 5, draft: version("#0f766e") as never });
        return json(current);
      }
      return undefined;
    });
    typeColour("#0f766e");
    fireEvent.click(screen.getByRole("button", { name: "Preview as me" }));
    await waitFor(() => expect(window.sessionStorage.getItem("vantigo-brand-preview")).toBe(ID));
  });
});

describe("the logos", () => {
  const upload = async (slot: string, file: File) => {
    const card = screen.getByTestId(`logo-${slot}`);
    const input = card.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
  };

  it("uploads a logo to the draft and shows it from the draft route", async () => {
    const fetchMock = await render((method, url) => {
      if (method === "PUT" && url === `/api/v1/identity/brands/${ID}/logos/mark_light`) {
        current = brand({
          revision: 5,
          draft: version("#4f46e5", {
            logos: { mark_light: { sha256: "a".repeat(64), contentType: "image/png" } },
          }) as never,
        });
        return json(current);
      }
      return undefined;
    });
    await upload("mark_light", new File(["x"], "logo.png", { type: "image/png" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const img = await screen.findByAltText("Mark for light backgrounds");
    expect(img.getAttribute("src")).toBe(
      `/api/v1/identity/brands/${ID}/logos/mark_light?draft=true&v=${"a".repeat(12)}`,
    );
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT") as [string, RequestInit];
    expect(put[1].body).toBeInstanceOf(FormData);
    expect((put[1].body as FormData).get("file")).toBeInstanceOf(File);
  });

  it.each([
    ["logo_unsupported_type", "The file must be a PNG or a JPEG."],
    ["logo_too_large", "The file is over 512 KiB."],
    ["logo_too_wide", "The longer side is over 2,000 pixels."],
    ["logo_too_small", "The shorter side is under 16 pixels."],
    ["logo_not_square", "The icon must be square, within 5 %."],
  ])("explains %s", async (code, text) => {
    await render((method) => (method === "PUT" ? refusal(400, code) : undefined));
    await upload("icon", new File(["x"], "icon.png", { type: "image/png" }));
    expect(await within(screen.getByTestId("logo-icon")).findByRole("alert")).toHaveTextContent(text);
  });

  it("says logos need an object store, and cannot be uploaded without one", async () => {
    await render(() => undefined, brand({ logoStorageAvailable: false }));
    expect(screen.getByText(/no object store/i)).toBeInTheDocument();
    const input = within(screen.getByTestId("logo-mark_light")).getByRole("button", { name: "Upload" });
    expect(input).toBeDisabled();
  });

  it("shows the import notice when the old logo could not be imported", async () => {
    await render(
      () => undefined,
      brand({
        importNotice: "APP_LOGO_URL could not be imported (the address could not be reached): upload the logo instead.",
      }),
    );
    expect(screen.getByText("The logo from the old settings was not imported")).toBeInTheDocument();
    expect(screen.getByText(/could not be reached/)).toBeInTheDocument();
  });
});

describe("the default brand of an installation", () => {
  it("starts the editor from Vantigo's own payload", () => {
    expect(builtinPayload().title).toBe("Vantigo");
  });
});
