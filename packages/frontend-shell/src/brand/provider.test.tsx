import { MantineProvider } from "@mantine/core";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppShellLayout } from "../app-shell-layout";
import { I18nProvider } from "../i18n";
import { vantigoLogo, vantigoLogoDark } from "../logo";
import { BRAND_MIRROR_KEY, readBrandMirror } from "./mirror";
import { BrandPreviewBanner } from "./preview-banner";
import { BRAND_PREVIEW_KEY, BrandProvider, getBrandPreview, setBrandPreview, useBrand } from "./provider";
import { BRAND_ALGORITHM } from "./scale";
import { type BrandPayload, builtinPayload, computeTokens } from "./tokens";

const teal = (over: Partial<BrandPayload> = {}): BrandPayload => ({
  ...builtinPayload(),
  brandId: "11111111-1111-1111-1111-111111111111",
  version: "11111111-1111-1111-1111-111111111111-3",
  name: "Acme",
  title: "Acme Portal",
  tokens: computeTokens("#0f766e", "lg"),
  logos: { mark_light: "/api/v1/identity/brand-assets/aaa.png", mark_dark: "/api/v1/identity/brand-assets/bbb.png" },
  support: { email: "help@acme.test" },
  ...over,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let answers: Record<string, () => Response>;
const fetchMock = vi.fn((input: RequestInfo | URL) => {
  const path = String(input);
  const key = Object.keys(answers).find((k) => path.startsWith(k));
  return Promise.resolve(key ? (answers[key] as () => Response)() : new Response(null, { status: 404 }));
});

const Probe = () => {
  const brand = useBrand();
  return (
    <div>
      <span data-testid="title">{brand.title}</span>
      <span data-testid="source">{brand.source}</span>
      <span data-testid="mark-light">{brand.markFor("light").src}</span>
      <span data-testid="mark-dark">{`${brand.markFor("dark").src}|${brand.markFor("dark").chip}`}</span>
    </div>
  );
};

const mount = (userId: string | null | undefined, sessionless = false) =>
  render(
    <MantineProvider env="test">
      <BrandProvider userId={userId} sessionless={sessionless}>
        <Probe />
      </BrandProvider>
    </MantineProvider>,
  );

beforeEach(() => {
  answers = {};
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
  window.localStorage.clear();
  window.sessionStorage.clear();
  document.documentElement.removeAttribute("style");
  document.title = "Vantigo";
});
afterEach(() => vi.unstubAllGlobals());

describe("the brand provider", () => {
  it("shows Vantigo's own brand until the server has answered", () => {
    mount(undefined);
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("asks for the person's own brand when signed in, and mirrors it for that person", async () => {
    answers["/api/v1/identity/brand"] = () => json(teal());
    mount("user-1");
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Acme Portal"));
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/identity/brand");
    expect(document.title).toBe("Acme Portal");
    const mirror = JSON.parse(window.localStorage.getItem(BRAND_MIRROR_KEY) as string);
    expect(mirror).toMatchObject({ v: 1, algorithm: BRAND_ALGORITHM, userId: "user-1", title: "Acme Portal" });
    expect(mirror.light["--mantine-color-vantigo-6"]).toBe("#0f766e");
    expect(mirror.dark["--mantine-color-vantigo-light"]).toMatch(/^rgba\(/);
  });

  it("asks for the public brand when nobody is signed in, and clears the mirror", async () => {
    window.localStorage.setItem(
      BRAND_MIRROR_KEY,
      JSON.stringify({ v: 1, algorithm: BRAND_ALGORITHM, userId: "user-1", title: "x" }),
    );
    answers["/api/v1/identity/brand/public"] = () => json(teal({ title: "Public Acme" }));
    mount(null);
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Public Acme"));
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/identity/brand/public");
    expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull();
  });

  it("clears the mirror when the person signs out", async () => {
    answers["/api/v1/identity/brand/public"] = () => json(teal({ title: "Public Acme" }));
    answers["/api/v1/identity/brand"] = () => json(teal());
    const { rerender } = mount("user-1");
    await waitFor(() => expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).not.toBeNull());
    rerender(
      <MantineProvider env="test">
        <BrandProvider userId={null}>
          <Probe />
        </BrandProvider>
      </MantineProvider>,
    );
    await waitFor(() => expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull());
  });

  it("ignores and removes the mirror of another person", () => {
    window.localStorage.setItem(
      BRAND_MIRROR_KEY,
      JSON.stringify({
        v: 1,
        algorithm: BRAND_ALGORITHM,
        userId: "someone-else",
        title: "Theirs",
        light: {},
        dark: {},
      }),
    );
    expect(readBrandMirror("user-1")).toBeNull();
    expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull();
  });

  it("ignores a mirror of a higher version or algorithm", () => {
    for (const bad of [
      { v: 2, algorithm: BRAND_ALGORITHM },
      { v: 1, algorithm: BRAND_ALGORITHM + 1 },
      { v: "1", algorithm: BRAND_ALGORITHM },
    ]) {
      window.localStorage.setItem(BRAND_MIRROR_KEY, JSON.stringify({ ...bad, userId: "user-1", title: "x" }));
      expect(readBrandMirror("user-1")).toBeNull();
    }
  });

  it("ignores a payload of a higher version", async () => {
    answers["/api/v1/identity/brand"] = () => json({ ...teal(), v: 2 });
    mount("user-1");
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
  });

  it("asks for nothing on a page that has no session (the pay page, a station)", async () => {
    mount(null, true);
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
  });

  it("removes the variables the pre-paint script set once the server has answered", async () => {
    document.documentElement.style.setProperty("--mantine-color-vantigo-6", "#ff0000");
    document.documentElement.style.setProperty("color", "red");
    answers["/api/v1/identity/brand"] = () => json(teal());
    mount("user-1");
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Acme Portal"));
    expect(document.documentElement.style.getPropertyValue("--mantine-color-vantigo-6")).toBe("");
    expect(document.documentElement.style.getPropertyValue("color")).toBe("red");
  });

  it("points the tab's icon at the brand's icon and back", async () => {
    document.head.insertAdjacentHTML("beforeend", '<link rel="icon" href="/favicon.png" id="ico">');
    answers["/api/v1/identity/brand"] = () => json(teal({ logos: { icon: "/api/v1/identity/brand-assets/ccc.png" } }));
    mount("user-1");
    await waitFor(() =>
      expect(document.getElementById("ico")?.getAttribute("href")).toBe("/api/v1/identity/brand-assets/ccc.png"),
    );
    document.getElementById("ico")?.remove();
  });
});

describe("the brand mark by colour scheme", () => {
  it("uses the dark mark in dark, the light mark in light, and a chip for a light mark on dark", async () => {
    answers["/api/v1/identity/brand"] = () => json(teal());
    const { unmount } = mount("user-1");
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Acme Portal"));
    expect(screen.getByTestId("mark-light")).toHaveTextContent("/brand-assets/aaa.png");
    expect(screen.getByTestId("mark-dark")).toHaveTextContent("/brand-assets/bbb.png|false");
    unmount();

    answers["/api/v1/identity/brand"] = () =>
      json(teal({ logos: { mark_light: "/api/v1/identity/brand-assets/aaa.png" } }));
    mount("user-1");
    await waitFor(() => expect(screen.getByTestId("mark-dark")).toHaveTextContent("/brand-assets/aaa.png|true"));
  });

  it("falls back to the bundled Vantigo mark for each scheme", () => {
    mount(undefined);
    expect(screen.getByTestId("mark-light")).toHaveTextContent(vantigoLogo);
    expect(screen.getByTestId("mark-dark")).toHaveTextContent(`${vantigoLogoDark}|false`);
  });

  it("is the header logo of the shell, by scheme, with the brand's title as its alt text", async () => {
    answers["/api/v1/identity/brand"] = () => json(teal());
    const shell = (scheme: "light" | "dark") => (
      <MantineProvider env="test" forceColorScheme={scheme}>
        <I18nProvider>
          <BrandProvider userId="user-1">
            <AppShellLayout>
              <p>content</p>
            </AppShellLayout>
          </BrandProvider>
        </I18nProvider>
      </MantineProvider>
    );
    const { unmount } = render(shell("dark"));
    const logo = await screen.findByAltText("Acme Portal");
    expect(logo).toHaveAttribute("src", "/api/v1/identity/brand-assets/bbb.png");
    unmount();
    render(shell("light"));
    expect(await screen.findByAltText("Acme Portal")).toHaveAttribute("src", "/api/v1/identity/brand-assets/aaa.png");
  });

  it("shows the brand's support contacts in the footer", async () => {
    answers["/api/v1/identity/brand"] = () => json(teal());
    render(
      <MantineProvider env="test">
        <I18nProvider>
          <BrandProvider userId="user-1">
            <AppShellLayout>
              <p>content</p>
            </AppShellLayout>
          </BrandProvider>
        </I18nProvider>
      </MantineProvider>,
    );
    expect(await screen.findByText("help@acme.test")).toHaveAttribute("href", "mailto:help@acme.test");
  });
});

describe("preview as me", () => {
  it("asks for the draft with the stored brand id and shows the banner with a way out", async () => {
    answers["/api/v1/identity/brand"] = () => json(teal({ source: "preview", name: "Draft Acme" }));
    setBrandPreview("11111111-1111-1111-1111-111111111111");
    render(
      <MantineProvider env="test">
        <I18nProvider>
          <BrandProvider userId="user-1">
            <BrandPreviewBanner />
          </BrandProvider>
        </I18nProvider>
      </MantineProvider>,
    );
    expect(await screen.findByRole("status")).toHaveTextContent("Draft Acme");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "/api/v1/identity/brand?preview=11111111-1111-1111-1111-111111111111",
    );
    // A preview is not mirrored: it is one tab's, one admin's.
    expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull();
    answers["/api/v1/identity/brand"] = () => json(teal());
    screen.getByRole("button").click();
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
    expect(getBrandPreview()).toBeNull();
  });

  it("stops previewing when the server refuses it", async () => {
    window.sessionStorage.setItem(BRAND_PREVIEW_KEY, "11111111-1111-1111-1111-111111111111");
    answers["/api/v1/identity/brand"] = () => new Response(null, { status: 403 });
    mount("user-1");
    await waitFor(() => expect(getBrandPreview()).toBeNull());
  });
});
