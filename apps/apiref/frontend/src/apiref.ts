// The API reference page's own script (API access design K27, K29): a key
// field above Scalar. With a key, it loads the document the key reaches and
// mounts Scalar with the key as the bearer token; without one, it loads the
// integrator's document under the visitor's session, or explains how to get
// access when there is none. The key lives in this closure only: nothing is
// written to localStorage, sessionStorage or a cookie, and Scalar is told not
// to persist it either.

import { type Copy, copyFor, INTRODUCTION_URL } from "./i18n";

export type ScalarInstance = { destroy: () => void };
export type ScalarGlobal = {
  createApiReference: (element: HTMLElement, configuration: Record<string, unknown>) => ScalarInstance;
};

export type Environment = {
  fetch: typeof fetch;
  scalar: ScalarGlobal;
  language: string | undefined;
};

// Both are relative to the page, which is /api under any base path.
export const DOCUMENT_URL = "api/openapi.json";
export const SESSION_DOCUMENT_URL = "api/openapi.json?view=api";

/**
 * Scalar's configuration: the document as content, and nothing that leaves
 * the installation — no default fonts (fonts.scalar.com), no proxy (none is
 * named, so requests go to the document's own server), no telemetry, no
 * agent or MCP chat (api.scalar.com) and no developer tools. The client
 * button is hidden as well: each operation's own "Test Request" is the client.
 */
export function scalarConfiguration(content: unknown, key: string | null): Record<string, unknown> {
  const configuration: Record<string, unknown> = {
    content,
    persistAuth: false,
    withDefaultFonts: false,
    telemetry: false,
    hideClientButton: true,
    showDeveloperTools: "never",
    agent: { disabled: true },
    mcp: { disabled: true },
    documentDownloadType: "json",
  };
  if (key !== null) {
    configuration.authentication = {
      preferredSecurityScheme: "bearerAuth",
      securitySchemes: { bearerAuth: { token: key } },
    };
  }
  return configuration;
}

type Elements = {
  form: HTMLFormElement;
  input: HTMLInputElement;
  clear: HTMLButtonElement;
  notice: HTMLParagraphElement;
  reference: HTMLDivElement;
};

function render(root: HTMLElement, copy: Copy): Elements {
  const doc = root.ownerDocument;
  const header = doc.createElement("header");
  header.className = "apiref-header";

  const title = doc.createElement("h1");
  title.textContent = copy.title;

  const form = doc.createElement("form");
  form.className = "apiref-key";
  form.autocomplete = "off";
  const label = doc.createElement("label");
  label.htmlFor = "apiref-key";
  label.textContent = copy.keyLabel;
  const input = doc.createElement("input");
  input.id = "apiref-key";
  input.type = "password";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = "vtg_…";
  const use = doc.createElement("button");
  use.type = "submit";
  use.textContent = copy.use;
  const clear = doc.createElement("button");
  clear.type = "button";
  clear.textContent = copy.clear;
  const hint = doc.createElement("small");
  hint.textContent = copy.keyHint;
  form.append(label, input, use, clear, hint);

  const notice = doc.createElement("p");
  notice.className = "apiref-notice";
  notice.setAttribute("role", "status");

  header.append(title, form, notice);
  const reference = doc.createElement("div");
  reference.className = "apiref-reference";
  root.replaceChildren(header, reference);
  return { form, input, clear, notice, reference };
}

/** Starts the page in root. It answers once the first document is shown. */
export async function start(root: HTMLElement, env: Environment): Promise<void> {
  const { copy, lang } = copyFor(env.language);
  root.ownerDocument.documentElement.lang = lang;
  root.ownerDocument.title = copy.title;
  const el = render(root, copy);

  let instance: ScalarInstance | null = null;
  // The key the mounted document was loaded with; null under the session.
  let mountedKey: string | null = null;
  // Each load supersedes the one before, so a slow answer never mounts over
  // a newer one.
  let generation = 0;

  const say = (text: string, link = false) => {
    el.notice.replaceChildren(text);
    if (link) {
      const a = root.ownerDocument.createElement("a");
      a.href = INTRODUCTION_URL;
      a.rel = "noopener noreferrer";
      a.textContent = copy.guide;
      el.notice.append(" ", a);
    }
  };

  const unmount = () => {
    instance?.destroy();
    instance = null;
    mountedKey = null;
    const fresh = root.ownerDocument.createElement("div");
    fresh.className = el.reference.className;
    el.reference.replaceWith(fresh);
    el.reference = fresh;
  };

  const mount = (content: unknown, key: string | null) => {
    unmount();
    instance = env.scalar.createApiReference(el.reference, scalarConfiguration(content, key));
    mountedKey = key;
  };

  const errorCode = async (response: Response): Promise<string> => {
    try {
      const body = (await response.json()) as { error?: { code?: unknown } };
      if (typeof body.error?.code === "string") return body.error.code;
    } catch {
      // Not the API's error body; the status says enough.
    }
    return String(response.status);
  };

  // Without a key: the integrator's document under the session, if there is
  // one, and otherwise the explanation. lead, when given, says first why the
  // page fell back here (a refused key).
  const loadSession = async (lead = "") => {
    const mine = ++generation;
    const sayAfter = (text: string, link = false) => say(lead ? `${lead} ${text}` : text, link);
    sayAfter(copy.loading);
    let response: Response;
    try {
      response = await env.fetch(SESSION_DOCUMENT_URL, {
        headers: { Accept: "application/json" },
        credentials: "same-origin",
      });
    } catch {
      if (mine === generation) sayAfter(copy.failed);
      return;
    }
    if (mine !== generation) return;
    if (response.status === 401 || response.status === 403) {
      unmount();
      sayAfter(copy.noAccess, true);
      return;
    }
    if (!response.ok) {
      unmount();
      sayAfter(copy.failed);
      return;
    }
    const content: unknown = await response.json();
    if (mine !== generation) return;
    mount(content, null);
    sayAfter(copy.session);
  };

  const loadKey = async (key: string) => {
    const mine = ++generation;
    say(copy.loading);
    let response: Response;
    try {
      // The key alone: a key request is judged as the key (K16), and no
      // cookie needs to travel with it.
      response = await env.fetch(DOCUMENT_URL, {
        headers: { Accept: "application/json", Authorization: `Bearer ${key}` },
        credentials: "omit",
      });
    } catch {
      if (mine === generation) await refuse(copy.failed);
      return;
    }
    if (mine !== generation) return;
    if (!response.ok) {
      const code = await errorCode(response);
      if (mine === generation) await refuse(copy.refused(code));
      return;
    }
    let content: unknown;
    try {
      content = await response.json();
    } catch {
      if (mine === generation) await refuse(copy.failed);
      return;
    }
    if (mine !== generation) return;
    mount(content, key);
    say(copy.key);
  };

  // A key that was refused, or whose document could not be loaded, leaves
  // nothing of an earlier key mounted: the page goes back to the session's
  // view, or the explanation, and says first what went wrong.
  const refuse = async (why: string) => {
    unmount();
    await loadSession(why);
  };

  const clear = async () => {
    el.input.value = "";
    await loadSession();
  };

  el.form.addEventListener("submit", (event) => {
    event.preventDefault();
    const key = el.input.value.trim();
    void (key === "" ? clear() : loadKey(key));
  });
  el.clear.addEventListener("click", () => {
    void clear();
  });
  // Emptying the field is clearing it, once a key's document is mounted.
  el.input.addEventListener("input", () => {
    if (el.input.value === "" && mountedKey !== null) void loadSession();
  });

  await loadSession();
}
