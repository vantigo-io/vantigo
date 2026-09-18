import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { useCodeSuggestion } from "./use-code-suggestion";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Answers the suggestion endpoint with a code derived from the name, so a name change moves the suggestion. */
const stubSuggestions = () =>
  stubFetch((url: RequestInfo | URL) => {
    const parsed = new URL(String(url), "http://localhost");
    if (parsed.pathname !== "/api/v1/projects/code-suggestion") {
      return Promise.resolve(new Response(null, { status: 404 }));
    }
    const customer = parsed.searchParams.get("customerId");
    const name = (parsed.searchParams.get("name") ?? "").replace(/[^a-z]/gi, "").toUpperCase();
    return Promise.resolve(jsonResponse(200, { code: `${customer ? "KVE" : "INT"}${name.slice(0, 4)}` }));
  });

type HookProps = { customerId?: number; internal: boolean; name: string; enabled: boolean };

const renderSuggestion = (initialProps: HookProps) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children);
  return renderHook(
    ({ customerId, internal, name, enabled }: HookProps) => useCodeSuggestion(customerId, internal, name, enabled),
    { initialProps, wrapper },
  );
};

/** Lets the 300 ms debounce elapse, so "nothing was asked" means the hook chose not to ask. */
const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 500)));

const waitForSuggestion = (result: { current: { suggestion: string | undefined } }, expected: string) =>
  waitFor(() => expect(result.current.suggestion).toBe(expected), { timeout: 3000 });

describe("useCodeSuggestion", () => {
  it("follows the suggestion, stops when the code is typed by hand, and follows again on request", async () => {
    stubSuggestions();
    const { result, rerender } = renderSuggestion({
      customerId: 1001,
      internal: false,
      name: "Website",
      enabled: true,
    });

    await waitForSuggestion(result, "KVEWEBS");
    expect(result.current.isFollowing).toBe(true);

    act(() => result.current.setManual("MYCODE"));
    expect(result.current.isFollowing).toBe(false);

    // A later suggestion still arrives; the form simply no longer applies it.
    rerender({ customerId: 1001, internal: false, name: "Rebuild", enabled: true });
    await waitForSuggestion(result, "KVEREBU");
    expect(result.current.isFollowing).toBe(false);

    act(() => result.current.useSuggestion());
    expect(result.current.isFollowing).toBe(true);
  });

  it("follows again when the code field is cleared", async () => {
    stubSuggestions();
    const { result } = renderSuggestion({ customerId: 1001, internal: false, name: "Website", enabled: true });

    await waitForSuggestion(result, "KVEWEBS");
    act(() => result.current.setManual("MYCODE"));
    expect(result.current.isFollowing).toBe(false);

    act(() => result.current.setManual("  "));
    expect(result.current.isFollowing).toBe(true);
  });

  it("never follows, and never asks for a suggestion, when it is not enabled", async () => {
    const fetchMock = stubSuggestions();
    const { result, rerender } = renderSuggestion({
      customerId: 1001,
      internal: false,
      name: "Website",
      enabled: false,
    });

    expect(result.current.isFollowing).toBe(false);
    rerender({ customerId: 1001, internal: false, name: "Website rebuild", enabled: false });
    await settle();

    expect(fetchMock.actualCalls).toHaveLength(0);
    expect(result.current.suggestion).toBeUndefined();
    expect(result.current.isFollowing).toBe(false);
  });

  it("asks for nothing until there is a name and either a customer or an internal project", async () => {
    const fetchMock = stubSuggestions();
    const { result, rerender } = renderSuggestion({ customerId: undefined, internal: false, name: "", enabled: true });

    rerender({ customerId: undefined, internal: false, name: "Website", enabled: true });
    await settle();
    expect(fetchMock.actualCalls).toHaveLength(0);

    rerender({ customerId: 1001, internal: false, name: "  ", enabled: true });
    await settle();
    expect(fetchMock.actualCalls).toHaveLength(0);

    rerender({ customerId: undefined, internal: true, name: "Website", enabled: true });
    await waitForSuggestion(result, "INTWEBS");
  });

  it("drops the customer from the question once the project is internal", async () => {
    const fetchMock = stubSuggestions();
    const { result } = renderSuggestion({ customerId: 1001, internal: true, name: "Offsite", enabled: true });

    await waitForSuggestion(result, "INTOFFS");
    expect(String(fetchMock.actualCalls[0]?.[0])).not.toContain("customerId");
  });
});
