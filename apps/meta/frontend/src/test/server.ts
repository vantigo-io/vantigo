import type { MetaPage, MetaSettings } from "../api/settings";
import { jsonResponse, problemResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

export const WEBHOOK_URL = "https://vantigo.example/api/v1/meta/webhook";
export const VERIFY_TOKEN = "vt_3f9a1c0d7b2e4a58";
export const NEW_VERIFY_TOKEN = "vt_b81e66f0a4c9d273";

/** The settings as `GET /settings` answers them on a fresh instance with a public host. */
export const settings = (overrides: Partial<MetaSettings> = {}): MetaSettings => ({
  appSecretSet: false,
  humanAgentGranted: false,
  verifyToken: VERIFY_TOKEN,
  publicHostConfigured: true,
  webhookUrl: WEBHOOK_URL,
  pages: [],
  status: {},
  ...overrides,
});

export const page = (overrides: Partial<MetaPage> = {}): MetaPage => ({
  pageId: "104200000000001",
  name: "Contoso Støtte",
  state: "active",
  addedAt: "2026-11-05T09:00:00Z",
  ...overrides,
});

export interface MetaServer {
  settings?: MetaSettings;
  /** Answers `PUT /settings` instead of echoing the request into the settings. */
  put?: () => Response;
  /** Answers `POST /pages` instead of connecting the Page. */
  addPage?: () => Response;
  failLoad?: boolean;
}

/** Stubs fetch with an in-memory Meta API: the settings, the Pages and the verify token. */
export const stubMetaApi = (server: MetaServer = {}): StubbedFetch => {
  let current = server.settings ?? settings();
  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (url === "/api/v1/meta/settings" && method === "GET") {
      return Promise.resolve(server.failLoad ? problemResponse(500, "boom") : jsonResponse(200, current));
    }
    if (url === "/api/v1/meta/settings" && method === "PUT") {
      if (server.put) return Promise.resolve(server.put());
      current = {
        ...current,
        ...(body.appId ? { appId: body.appId } : {}),
        ...(body.appSecret ? { appSecretSet: true } : {}),
        ...(typeof body.humanAgentGranted === "boolean" ? { humanAgentGranted: body.humanAgentGranted } : {}),
      };
      return Promise.resolve(jsonResponse(200, current));
    }
    if (url === "/api/v1/meta/settings/verify-token" && method === "POST") {
      current = { ...current, verifyToken: NEW_VERIFY_TOKEN };
      return Promise.resolve(jsonResponse(200, current));
    }
    if (url === "/api/v1/meta/pages" && method === "POST") {
      if (server.addPage) return Promise.resolve(server.addPage());
      const added = page({ name: "Contoso Salg", pageId: "104200000000002" });
      current = { ...current, pages: [...current.pages, added] };
      return Promise.resolve(jsonResponse(201, added));
    }
    const removal = /^\/api\/v1\/meta\/pages\/([^/]+)$/.exec(url);
    if (removal && method === "DELETE") {
      current = { ...current, pages: current.pages.filter((one) => one.pageId !== removal[1]) };
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
};
