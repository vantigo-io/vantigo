import { afterEach, describe, expect, it, vi } from "vitest";
import {
  authorizeQuery,
  captureAuthorizeQuery,
  oauthReturnKey,
  oauthReturnMaxAgeMs,
  oauthReturnTouchedKey,
  peekOAuthReturn,
  rememberOAuthReturn,
  restoreAuthorizeAddress,
  takeOAuthReturn,
  touchOAuthReturn,
} from "./oauth-return";

const now = 1_800_000_000_000;
const query = "response_type=code&client_id=https%3A%2F%2Fclaude.ai%2Foauth%2Fmeta&state=%22abc%22";

const stored = (value: unknown) => sessionStorage.setItem(oauthReturnKey, JSON.stringify(value));

afterEach(() => {
  sessionStorage.clear();
  captureAuthorizeQuery({ pathname: "/", search: "" });
});

describe("the consent page's return after sign-in (OAuth connections design A8)", () => {
  it("gives back the page and its query once, and removes the entry", () => {
    rememberOAuthReturn(query, sessionStorage, now);
    expect(takeOAuthReturn(sessionStorage, now + 60_000)).toBe(`/oauth/authorize?${query}`);
    expect(sessionStorage.getItem(oauthReturnKey)).toBeNull();
    expect(takeOAuthReturn(sessionStorage, now + 60_000)).toBeUndefined();
  });

  it("is honoured for ten minutes only, and not from the future", () => {
    rememberOAuthReturn(query, sessionStorage, now);
    expect(takeOAuthReturn(sessionStorage, now + oauthReturnMaxAgeMs)).toBeUndefined();
    rememberOAuthReturn(query, sessionStorage, now);
    expect(takeOAuthReturn(sessionStorage, now - 1)).toBeUndefined();
    // Removed even when refused, so a stale one cannot linger.
    expect(sessionStorage.getItem(oauthReturnKey)).toBeNull();
  });

  it.each([
    ["another page", "/workspace/api-access"],
    ["a page beside it", "/oauth/authorizex?a=1"],
    ["a page below it", "/oauth/authorize/next"],
    ["a dot segment", "/oauth/authorize/../../sign-in"],
    ["another host", "//evil.example/oauth/authorize?a=1"],
    ["an absolute URL", "https://evil.example/oauth/authorize?a=1"],
    ["a backslash host", "/\\evil.example/oauth/authorize"],
    ["a script", "javascript:alert(1)"],
    ["a fragment", "/oauth/authorize?a=1#x"],
    ["an encoded path", "/oauth%2Fauthorize?a=1"],
    ["an overlong query", `/oauth/authorize?${"a".repeat(9000)}`],
  ])("refuses %s", (_name, path) => {
    stored({ path, at: now });
    expect(takeOAuthReturn(sessionStorage, now)).toBeUndefined();
    expect(sessionStorage.getItem(oauthReturnKey)).toBeNull();
  });

  it.each([
    ["not JSON", "{"],
    ["no timestamp", JSON.stringify({ path: "/oauth/authorize?a=1" })],
    ["a timestamp that is text", JSON.stringify({ path: "/oauth/authorize?a=1", at: String(now) })],
    ["a path that is not text", JSON.stringify({ path: ["/oauth/authorize"], at: now })],
    ["a bare string", JSON.stringify("/oauth/authorize?a=1")],
  ])("refuses an entry that is %s", (_name, raw) => {
    sessionStorage.setItem(oauthReturnKey, raw);
    expect(takeOAuthReturn(sessionStorage, now)).toBeUndefined();
  });

  it("reads the query the page loaded with, before the router rewrites the address", () => {
    captureAuthorizeQuery({ pathname: "/oauth/authorize", search: `?${query}` });
    expect(authorizeQuery()).toBe(query);
    captureAuthorizeQuery({ pathname: "/workspace", search: "?tab=x" });
    expect(authorizeQuery()).toBe("");
  });

  it("peeks at a waiting entry without taking it", () => {
    rememberOAuthReturn(query, sessionStorage, now);
    expect(peekOAuthReturn(sessionStorage, now + 1000)).toBe(`/oauth/authorize?${query}`);
    expect(sessionStorage.getItem(oauthReturnKey)).not.toBeNull();
    expect(peekOAuthReturn(sessionStorage, now + oauthReturnMaxAgeMs)).toBeUndefined();
  });

  it("starts a valid entry's ten minutes again without rewriting it, and leaves an invalid one to expire", () => {
    rememberOAuthReturn(query, sessionStorage, now);
    const entry = sessionStorage.getItem(oauthReturnKey);
    touchOAuthReturn(sessionStorage, now + 9 * 60_000);
    expect(sessionStorage.getItem(oauthReturnKey)).toBe(entry);
    expect(sessionStorage.getItem(oauthReturnTouchedKey)).toBe(String(now + 9 * 60_000));
    expect(peekOAuthReturn(sessionStorage, now + 18 * 60_000)).toBe(`/oauth/authorize?${query}`);
    expect(peekOAuthReturn(sessionStorage, now + 19 * 60_000)).toBeUndefined();
    expect(takeOAuthReturn(sessionStorage, now + 18 * 60_000)).toBe(`/oauth/authorize?${query}`);
    // Taking it clears the touched time too.
    expect(sessionStorage.getItem(oauthReturnTouchedKey)).toBeNull();

    stored({ path: "/workspace", at: now });
    touchOAuthReturn(sessionStorage, now + 1000);
    expect(sessionStorage.getItem(oauthReturnTouchedKey)).toBeNull();
  });

  it("does nothing with a touched time alone, and a new entry starts without the old one", () => {
    sessionStorage.setItem(oauthReturnTouchedKey, String(now));
    expect(peekOAuthReturn(sessionStorage, now + 1000)).toBeUndefined();
    expect(takeOAuthReturn(sessionStorage, now + 1000)).toBeUndefined();
    sessionStorage.setItem(oauthReturnTouchedKey, String(now + 9 * 60_000));
    rememberOAuthReturn(query, sessionStorage, now);
    expect(sessionStorage.getItem(oauthReturnTouchedKey)).toBeNull();
    expect(peekOAuthReturn(sessionStorage, now + 11 * 60_000)).toBeUndefined();
  });

  it.each([
    ["in the future", String(now + 60 * 60_000)],
    ["not a number", "soon"],
    ["infinite", "Infinity"],
  ])("ignores a touched time %s", (_name, touched) => {
    rememberOAuthReturn(query, sessionStorage, now);
    sessionStorage.setItem(oauthReturnTouchedKey, touched);
    expect(peekOAuthReturn(sessionStorage, now + 5 * 60_000)).toBe(`/oauth/authorize?${query}`);
    expect(peekOAuthReturn(sessionStorage, now + oauthReturnMaxAgeMs)).toBeUndefined();
  });
});

describe("the consent page's address (OAuth connections design A27)", () => {
  const raw = "client_id=https%3A%2F%2Fclaude.ai%2Fm&scope=read%20write&state=1e3";

  it("puts back the query the router rewrote, so a reload reads the request unchanged", () => {
    const history = { state: { key: "k" }, replaceState: vi.fn() };
    restoreAuthorizeAddress(
      raw,
      {
        pathname: "/oauth/authorize",
        search: "?client_id=https%3A%2F%2Fclaude.ai%2Fm&scope=read+write&state=1000",
        hash: "",
      },
      history,
    );
    expect(history.replaceState).toHaveBeenCalledWith({ key: "k" }, "", `/oauth/authorize?${raw}`);
  });

  it("leaves an exact address and other pages alone", () => {
    const history = { state: null, replaceState: vi.fn() };
    restoreAuthorizeAddress(raw, { pathname: "/oauth/authorize", search: `?${raw}`, hash: "" }, history);
    restoreAuthorizeAddress(raw, { pathname: "/workspace", search: "?x=1", hash: "" }, history);
    expect(history.replaceState).not.toHaveBeenCalled();
  });

  it("captures the query with its 1e3 and %20 exactly as sent", () => {
    captureAuthorizeQuery({ pathname: "/oauth/authorize", search: `?${raw}` });
    expect(authorizeQuery()).toBe(raw);
  });
});
