import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { DIRECTORY_SEARCH_LIMIT, type DirectoryMatches, type DirectoryUser } from "../api/directory";
import { stubFetch } from "./fetch";

/** Identity's directory endpoints as a test shapes them. */
export interface DirectoryServer {
  /** The accounts `GET /owner/sso/directory/users` searches (a case-insensitive substring of the name, mail or principal name), or what it answers instead. */
  users?: DirectoryUser[] | Response;
  /** What `GET /owner/sso/directory/matches` answers; no proposal by default. */
  matches?: DirectoryMatches | Response;
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** An identity refusal as the server writes it: `{error: {code, message}}`. */
export const identityRefusal = (status: number, code: string) =>
  json(status, { error: { code, message: `server text for ${code}` } });

/** Stubs fetch with identity's two directory reads over `server`; anything else is a 404. */
export const stubDirectoryApi = (server: DirectoryServer) =>
  stubFetch((input: RequestInfo | URL) => {
    const path = String(input);
    if (path.startsWith("/api/v1/identity/owner/sso/directory/users?")) {
      if (server.users instanceof Response) return Promise.resolve(server.users.clone());
      const search = (new URLSearchParams(path.split("?")[1]).get("search") ?? "").trim().toLowerCase();
      const hits = (server.users ?? [])
        .filter((one) =>
          [one.displayName, one.mail ?? "", one.userPrincipalName].some((value) =>
            value.toLowerCase().includes(search),
          ),
        )
        .slice(0, DIRECTORY_SEARCH_LIMIT);
      return Promise.resolve(json(200, { users: hits }));
    }
    if (path === "/api/v1/identity/owner/sso/directory/matches") {
      if (server.matches instanceof Response) return Promise.resolve(server.matches.clone());
      return Promise.resolve(
        json(200, server.matches ?? { matches: [], candidates: 0, ambiguous: 0, truncated: false }),
      );
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

/** One account in the directory, enabled and held by nobody. */
export const directoryUser = (overrides: Partial<DirectoryUser> = {}): DirectoryUser => ({
  objectId: "44444444-4444-4444-8444-444444444444",
  displayName: "Kari Nordmann",
  mail: "kari@contoso.no",
  userPrincipalName: "kari@contoso.no",
  enabled: true,
  ...overrides,
});

/** Mounts a component under Mantine (env="test") and a fresh query client that never retries. */
export const renderWithProviders = (ui: ReactNode) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </MantineProvider>,
  );
};
