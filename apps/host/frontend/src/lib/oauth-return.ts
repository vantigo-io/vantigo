import { appPathname } from "./public-paths";

/**
 * The consent page's address and its return after sign-in (OAuth connections
 * design C13, A8). A client sends the browser to `/oauth/authorize?…`; a
 * visitor without a session is sent to sign in, and the page's own path and
 * query wait in `sessionStorage` under `oauth.return`. The root takes the entry
 * once its session and MFA-enrolment gates pass, whichever way the person
 * signed in, and goes back. No URL ever carries a return-to, and the entry is
 * honoured only in its one shape — this page, on this origin, younger than ten
 * minutes — so it cannot be made to send anyone anywhere else.
 */
export const authorizePath = "/oauth/authorize";
export const oauthReturnKey = "oauth.return";
/** How long the entry is honoured (C13). */
export const oauthReturnMaxAgeMs = 10 * 60_000;
/** The longest query the consent operations take (A9), with room for the path. */
const maxReturnLength = authorizePath.length + 1 + 8192;

/** `/oauth/authorize` with an optional query and nothing else: no fragment, no second path. */
const returnShape = /^\/oauth\/authorize(\?[^#]*)?$/;

/** Stores the consent page's query for the return after sign-in. */
export const rememberOAuthReturn = (query: string, storage: Storage = sessionStorage, now: number = Date.now()) => {
  const path = query ? `${authorizePath}?${query}` : authorizePath;
  try {
    storage.setItem(oauthReturnKey, JSON.stringify({ path, at: now }));
  } catch {
    /* storage refused (private mode, quota): the person comes back by the client's link */
  }
};

/**
 * Takes the entry, which is removed whatever it holds, and answers the path to
 * go back to when it is the consent page's and young enough; otherwise
 * undefined.
 */
export const takeOAuthReturn = (storage: Storage = sessionStorage, now: number = Date.now()): string | undefined => {
  let raw: string | null;
  try {
    raw = storage.getItem(oauthReturnKey);
    storage.removeItem(oauthReturnKey);
  } catch {
    return undefined;
  }
  if (!raw) return undefined;
  let entry: unknown;
  try {
    entry = JSON.parse(raw);
  } catch {
    return undefined;
  }
  if (!entry || typeof entry !== "object") return undefined;
  const { path, at } = entry as { path?: unknown; at?: unknown };
  if (typeof path !== "string" || typeof at !== "number" || !Number.isFinite(at)) return undefined;
  if (now < at || now - at >= oauthReturnMaxAgeMs) return undefined;
  if (path.length > maxReturnLength || !returnShape.test(path)) return undefined;
  // The same path once a URL parser has read it: nothing that resolves elsewhere.
  const parsed = new URL(path, "https://vantigo.invalid");
  if (parsed.origin !== "https://vantigo.invalid" || parsed.pathname !== authorizePath || parsed.hash) return undefined;
  return path;
};

/**
 * The query string the consent page was loaded with, as the browser received
 * it. The router rewrites a URL's search into its own canonical form when it
 * mounts, which may change a value (a quoted or numeric-looking `state`, a
 * repeated parameter), and the consent operations must judge the request
 * exactly as the client sent it; so main.tsx reads it before the router exists.
 */
let loadedQuery: string | undefined;

/** Called by main.tsx before the router is made. */
export const captureAuthorizeQuery = (location: Pick<Location, "pathname" | "search">) => {
  loadedQuery = appPathname(location.pathname) === authorizePath ? location.search.replace(/^\?/, "") : undefined;
};

/** The consent page's query without its question mark: the one captured at load, else the address bar's. */
export const authorizeQuery = (): string => loadedQuery ?? window.location.search.replace(/^\?/, "");
