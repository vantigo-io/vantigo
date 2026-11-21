import { afterEach, describe, expect, it } from "vitest";
import {
  authorizeQuery,
  captureAuthorizeQuery,
  oauthReturnKey,
  oauthReturnMaxAgeMs,
  rememberOAuthReturn,
  takeOAuthReturn,
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
});
