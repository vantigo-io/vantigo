import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { domainOfEmail, looksLikeDomain, useDomainBrand } from "./domain-brand";
import { BRAND_MIRROR_KEY } from "./mirror";
import { BrandProvider, useBrand } from "./provider";
import { type BrandPayload, builtinPayload, computeTokens } from "./tokens";

const brandOf = (name: string, primary: string, over: Partial<BrandPayload> = {}): BrandPayload => ({
  ...builtinPayload(),
  brandId: `id-${name}`,
  version: `id-${name}-1`,
  name,
  title: `${name} Portal`,
  tokens: computeTokens(primary, "md"),
  ...over,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The server's answer by domain: nordlys.example is attached, fjord.example too, anything else is the default. */
const answers: Record<string, () => Response> = {};
const fetchMock = vi.fn((input: RequestInfo | URL) => {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname.endsWith("/brand/public")) {
    const domain = url.searchParams.get("domain") ?? "";
    const answer = answers[domain];
    if (answer) return Promise.resolve(answer());
    return Promise.resolve(json({ ...builtinPayload(), matched: false }));
  }
  return Promise.resolve(new Response(null, { status: 404 }));
});

const Page = () => {
  const [email, setEmail] = useState("");
  const domainBrand = useDomainBrand(email);
  const brand = useBrand();
  return (
    <div>
      <input aria-label="email" value={email} onChange={(e) => setEmail(e.target.value)} onBlur={domainBrand.onBlur} />
      <span data-testid="title">{brand.title}</span>
    </div>
  );
};

const mount = () =>
  render(
    <MantineProvider env="test">
      <BrandProvider userId={null}>
        <Page />
      </BrandProvider>
    </MantineProvider>,
  );

const type = (value: string) => fireEvent.change(screen.getByLabelText("email"), { target: { value } });
const settle = (ms = 400) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
const lookups = () => fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/brand/public?domain="));
const fading = () => document.documentElement.hasAttribute("data-vantigo-brand-fade");

const setReducedMotion = (reduce: boolean) =>
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: reduce && query.includes("reduce"),
      media: query,
      addEventListener() {},
      removeEventListener() {},
    })),
  );

beforeEach(() => {
  vi.useFakeTimers();
  fetchMock.mockClear();
  for (const k of Object.keys(answers)) delete answers[k];
  answers["nordlys.example"] = () => json({ ...brandOf("Nordlys", "#0f766e", { source: "rule" }), matched: true });
  answers["fjord.example"] = () => json({ ...brandOf("Fjord", "#b45309", { source: "rule" }), matched: true });
  vi.stubGlobal("fetch", fetchMock);
  setReducedMotion(false);
  window.localStorage.clear();
  document.documentElement.removeAttribute("style");
  document.documentElement.removeAttribute("data-vantigo-brand-fade");
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("the domain of a typed address", () => {
  it("is what follows the @, and looks like a domain only when it has a dot and a final label of 2 letters", () => {
    expect(domainOfEmail("Anna@Nordlys.Example.")).toBe("nordlys.example");
    expect(domainOfEmail("anna")).toBeNull();
    expect(looksLikeDomain("nordlys.example")).toBe(true);
    expect(looksLikeDomain("nordlys.xn--p1ai")).toBe(true);
    for (const bad of [
      "",
      "nordlys",
      "nordlys.e",
      "nordlys.",
      "-a.example",
      "a-.example",
      "a b.example",
      "nordlys.123",
    ]) {
      expect(looksLikeDomain(bad), bad).toBe(false);
    }
  });
});

describe("the sign-in page's brand by e-mail domain", () => {
  it("starts in the default brand and asks nothing while the address is partial", async () => {
    mount();
    for (const partial of ["a", "anna", "anna@", "anna@nord", "anna@nordlys", "anna@nordlys.e"]) {
      type(partial);
      await settle();
    }
    expect(lookups()).toEqual([]);
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
  });

  it("asks once after the pause with the domain only, never the local part, and fades in the matched brand", async () => {
    mount();
    type("anna@nordlys.example");
    await settle(399);
    expect(lookups()).toEqual([]);
    await settle(1);
    expect(lookups()).toEqual(["/api/v1/identity/brand/public?domain=nordlys.example"]);
    expect(lookups().join()).not.toContain("anna");
    expect(lookups().join()).not.toContain("@");
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    expect(fading()).toBe(true);
    await settle(500);
    expect(fading()).toBe(false);
    expect(document.title).toBe("Nordlys Portal");
  });

  it("asks on blur without waiting, and once per distinct domain", async () => {
    mount();
    type("anna@nordlys.example");
    fireEvent.blur(screen.getByLabelText("email"));
    await settle(0);
    expect(lookups()).toHaveLength(1);
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    type("kari@nordlys.example"); // another person, the same domain
    await settle();
    expect(lookups()).toHaveLength(1);
  });

  it("fades to another domain's brand, and back to the default when the domain is not attached or the field is cleared", async () => {
    mount();
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    type("a@fjord.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Fjord Portal");
    expect(fading()).toBe(true);
    type("a@elsewhere.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    expect(lookups()).toHaveLength(3); // nordlys again came from memory
    type("");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
  });

  it("changes instantly with reduced motion", async () => {
    setReducedMotion(true);
    mount();
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    expect(fading()).toBe(false);
  });

  it("keeps the brand it has when the lookup is refused or fails, and asks again for that domain later", async () => {
    mount();
    answers["nordlys.example"] = () => json({ error: { code: "rate_limited" } }, 429);
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Vantigo");
    answers["nordlys.example"] = () => json({ ...brandOf("Nordlys", "#0f766e", { source: "rule" }), matched: true });
    type("a@fjord.example");
    await settle();
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
  });

  it("never writes the pre-paint mirror, and leaves the brand behind when the page goes", async () => {
    const view = mount();
    type("a@nordlys.example");
    await settle();
    expect(screen.getByTestId("title")).toHaveTextContent("Nordlys Portal");
    expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull();
    view.unmount();
    expect(window.localStorage.getItem(BRAND_MIRROR_KEY)).toBeNull();
    expect(fading()).toBe(false);
  });
});
