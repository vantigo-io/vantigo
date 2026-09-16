/** Support contact details shown in the shell footer when configured. */
export interface AppSupport {
  email?: string;
  phone?: string;
  url?: string;
}

/** Runtime application configuration (base path + whitelabeling). */
export interface AppConfig {
  /** The base path the host is served under, with a trailing slash (e.g. "/vantigo/"). */
  basePath: string;
  /** The application title (App__Title, defaults to the app name). */
  title: string;
  /** Custom logo URL (App__LogoUrl); undefined means the bundled Vantigo logo. */
  logoUrl?: string;
  support: AppSupport;
  /**
   * The module names the backend enabled (its MODULES allowlist). Undefined
   * when nothing was injected — the Vite dev server serves an untemplated
   * index.html — which callers treat as "every module". An injected empty
   * list means none.
   */
  modules?: readonly string[];
}

interface InjectedAppConfig {
  basePath?: string;
  title?: string;
  logoUrl?: string | null;
  support?: { email?: string | null; phone?: string | null; url?: string | null };
  modules?: string[] | null;
}

declare global {
  interface Window {
    /** Runtime config injected into index.html by the API (SpaIndexDocument). */
    __VANTIGO_APP__?: InjectedAppConfig;
  }
}

let devDefaults: Partial<AppConfig> = {};

/**
 * Sets the development-time defaults (most importantly the app title) used when
 * the backend has not injected a runtime config — i.e. under the Vite dev
 * server, where index.html is not templated. Call once at bootstrap.
 */
export function initAppConfig(defaults: { title: string }): void {
  devDefaults = defaults;
}

const buildTimeBase = (): string => (import.meta as ImportMeta & { env?: { BASE_URL?: string } }).env?.BASE_URL ?? "/";

/**
 * The runtime application configuration. Prefers the values the backend injects
 * into the document at serve time (reflecting the App__* settings) and falls
 * back to build-time/dev defaults.
 */
export function appConfig(): AppConfig {
  const injected = typeof window !== "undefined" ? window.__VANTIGO_APP__ : undefined;
  return {
    basePath: injected?.basePath || buildTimeBase(),
    title: injected?.title || devDefaults.title || "Vantigo",
    logoUrl: injected?.logoUrl ?? undefined,
    support: {
      email: injected?.support?.email ?? undefined,
      phone: injected?.support?.phone ?? undefined,
      url: injected?.support?.url ?? undefined,
    },
    modules: injected?.modules ?? undefined,
  };
}

/** Whether any support contact detail is configured. */
export function hasSupportContact(config: AppConfig = appConfig()): boolean {
  const { email, phone, url } = config.support;
  return Boolean(email || phone || url);
}

/**
 * The base path the app is served under, with a trailing slash (e.g.
 * "/customers/" or "/").
 */
export function runtimeBase(): string {
  return appConfig().basePath;
}

/**
 * Joins the app's base path with an app-root-relative path. Use for raw
 * fetch/window.location/anchor URLs that bypass the router (the TanStack
 * Router basepath handles router navigation automatically).
 *
 * appUrl("/api/v1/identity/session") => "/vantigo/api/v1/identity/session" when base is "/vantigo/".
 */
export function appUrl(path: string): string {
  const base = runtimeBase();
  const normalizedBase = base.endsWith("/") ? base.slice(0, -1) : base;
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${normalizedBase}${normalizedPath}` || "/";
}
