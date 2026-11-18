import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DOCUMENT_URL, type Environment, type ScalarGlobal, SESSION_DOCUMENT_URL, start } from "./apiref";

const KEY = "vtg_abcdEFGHijklMNOPqrstUVWXyz0123456789ABCDEFG000000";
const keyDocument = { openapi: "3.0.3", info: { title: "key" }, paths: {} };
const sessionDocument = { openapi: "3.0.3", info: { title: "session" }, paths: {} };

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

type Mounted = { configuration: Record<string, unknown>; destroyed: boolean };

function setup(options: { session: boolean; keyStatus?: number }) {
  const mounted: Mounted[] = [];
  const scalar: ScalarGlobal = {
    createApiReference: (_element, configuration) => {
      const m: Mounted = { configuration, destroyed: false };
      mounted.push(m);
      return {
        destroy: () => {
          m.destroyed = true;
        },
      };
    },
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    if (String(input) === DOCUMENT_URL && headers.get("Authorization") !== null) {
      const status = options.keyStatus ?? 200;
      return status === 200
        ? json(200, keyDocument)
        : json(status, { error: { code: "api_key_expired", message: "expired" } });
    }
    if (String(input) === SESSION_DOCUMENT_URL) {
      return options.session ? json(200, sessionDocument) : json(401, { error: { code: "unauthenticated" } });
    }
    return json(404, {});
  });
  const env: Environment = { fetch: fetchMock as unknown as typeof fetch, scalar, language: "en-GB" };
  const root = document.createElement("div");
  document.body.append(root);
  return { env, root, mounted, fetchMock };
}

const input = (root: HTMLElement) => root.querySelector("input") as HTMLInputElement;
const submit = (root: HTMLElement) => (root.querySelector("form") as HTMLFormElement).requestSubmit();
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("the API reference page", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => {
    document.body.replaceChildren();
    vi.restoreAllMocks();
  });

  it("sends the key in the Authorization header and mounts Scalar with it", async () => {
    const { env, root, mounted, fetchMock } = setup({ session: true });
    await start(root, env);
    input(root).value = KEY;
    submit(root);
    await flush();

    const call = fetchMock.mock.calls.find(([url]) => String(url) === DOCUMENT_URL);
    expect(call).toBeDefined();
    const headers = new Headers(call?.[1]?.headers);
    expect(headers.get("Authorization")).toBe(`Bearer ${KEY}`);
    expect(call?.[1]?.credentials).toBe("omit");
    expect(String(call?.[0])).not.toContain(KEY);

    const last = mounted.at(-1);
    expect(last?.configuration.content).toEqual(keyDocument);
    expect(last?.configuration.persistAuth).toBe(false);
    expect(last?.configuration.withDefaultFonts).toBe(false);
    expect(last?.configuration.telemetry).toBe(false);
    expect(last?.configuration.proxyUrl).toBeUndefined();
    expect(last?.configuration.authentication).toEqual({
      preferredSecurityScheme: "bearerAuth",
      securitySchemes: { bearerAuth: { token: KEY } },
    });
  });

  it("writes nothing to localStorage, sessionStorage or a cookie", async () => {
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    const { env, root } = setup({ session: true });
    await start(root, env);
    input(root).value = KEY;
    submit(root);
    await flush();
    (root.querySelectorAll("button")[1] as HTMLButtonElement).click();
    await flush();

    expect(setItem).not.toHaveBeenCalled();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(document.cookie).not.toContain(KEY);
  });

  it("remounts without the key when the field is cleared", async () => {
    const { env, root, mounted } = setup({ session: true });
    await start(root, env);
    input(root).value = KEY;
    submit(root);
    await flush();
    expect(mounted).toHaveLength(2);

    input(root).value = "";
    input(root).dispatchEvent(new Event("input"));
    await flush();

    expect(mounted).toHaveLength(3);
    expect(mounted[1]?.destroyed).toBe(true);
    expect(mounted[2]?.configuration.content).toEqual(sessionDocument);
    expect(mounted[2]?.configuration.authentication).toBeUndefined();
    expect(JSON.stringify(mounted[2]?.configuration)).not.toContain(KEY);
  });

  it("the Clear button remounts without the key too", async () => {
    const { env, root, mounted } = setup({ session: true });
    await start(root, env);
    input(root).value = KEY;
    submit(root);
    await flush();
    (root.querySelectorAll("button")[1] as HTMLButtonElement).click();
    await flush();

    expect(input(root).value).toBe("");
    expect(mounted.at(-1)?.configuration.authentication).toBeUndefined();
  });

  it("under a session, shows the integrator's document and says try-it uses the session", async () => {
    const { env, root, mounted, fetchMock } = setup({ session: true });
    await start(root, env);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(SESSION_DOCUMENT_URL);
    expect(mounted[0]?.configuration.content).toEqual(sessionDocument);
    expect(root.querySelector("[role=status]")?.textContent).toContain("your own session");
  });

  it("without a session or a key, explains and links the introduction", async () => {
    const { env, root, mounted } = setup({ session: false });
    await start(root, env);
    expect(mounted).toHaveLength(0);
    const link = root.querySelector("a");
    expect(link?.getAttribute("href")).toBe("https://docs.vantigo.io/en/reference/api-introduction/");
    expect(root.textContent).toContain("Sign in to Vantigo, or enter an API key");
  });

  it("says why a key was refused and mounts nothing with it", async () => {
    const { env, root, mounted } = setup({ session: false, keyStatus: 401 });
    await start(root, env);
    input(root).value = KEY;
    submit(root);
    await flush();
    expect(mounted).toHaveLength(0);
    expect(root.querySelector("[role=status]")?.textContent).toContain("api_key_expired");
  });

  it("speaks Norwegian to a Norwegian browser", async () => {
    const { env, root } = setup({ session: true });
    await start(root, { ...env, language: "nb-NO" });
    expect(document.documentElement.lang).toBe("nb");
    expect(root.querySelector("label")?.textContent).toBe("API-nøkkel");
    expect(root.querySelector("[role=status]")?.textContent).toContain("din egen økt");
  });
});
