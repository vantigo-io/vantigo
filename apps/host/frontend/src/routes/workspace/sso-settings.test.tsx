import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { formatDate } from "@vantigo/frontend-shell";
import { describe, expect, it, vi } from "vitest";
import type { SsoSettings } from "../../api/sso";
import { stubFetch } from "../../test/fetch";
import { renderRoute } from "../../test/render-route";
import { SsoSettingsCard } from "./-sso-settings";
import "../../i18n";

const authority = "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0";
const otherAuthority = "https://login.microsoftonline.com/33333333-3333-3333-3333-333333333333/v2.0";
const quote = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** Entra on with a stored secret, SCIM on in the middle of a rotation's overlap, as GET /owner/sso answers. */
const entraWithOverlap = (): SsoSettings => ({
  version: 7,
  oidc: {
    enabled: true,
    provider: "entra",
    authority,
    clientId: "22222222-2222-2222-2222-222222222222",
    clientSecretSetAt: "2026-10-01T08:00:00Z",
    allowedDomains: [],
    displayName: "Contoso SSO",
    callbackUrl: "https://vantigo.example.com/api/v1/identity/oidc/callback",
  },
  scim: {
    enabled: true,
    tokenSetAt: "2026-10-18T08:00:00Z",
    previousExpiresAt: "2026-10-21T08:00:00Z",
    endpointUrl: "https://vantigo.example.com/api/v1/identity/scim/v2",
  },
  importedFromEnvironment: true,
  lastOidcSignInAt: "2026-10-19T09:30:00Z",
  lastScimRequestAt: "2026-10-19T10:00:00Z",
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const refusal = (status: number, code: string, fields?: Record<string, string[]>) =>
  json({ error: { code, message: `server text for ${code}`, fields } }, status);

type Handler = (method: string, url: string, init: RequestInit | undefined) => Response | undefined;

/** A fake identity API: GET /owner/sso answers the row, every other request goes to `handle`. */
const renderCard = async (handle: Handler = () => undefined, row = entraWithOverlap()) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    if (method === "GET" && url === "/api/v1/identity/owner/sso") return Promise.resolve(json(row));
    return Promise.resolve(handle(method, url, init) ?? new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/overview", SsoSettingsCard, queryClient);
  await screen.findByDisplayValue(row.oidc.callbackUrl);
  return fetchMock;
};

/** The requests the card wrote, in order: method, url, headers and parsed body. */
const writes = (fetchMock: Awaited<ReturnType<typeof renderCard>>) =>
  fetchMock.mock.calls
    .map(([input, request]) => {
      return {
        method: request?.method ?? "GET",
        url: String(input),
        headers: (request?.headers ?? {}) as Record<string, string>,
        body: typeof request?.body === "string" ? (JSON.parse(request.body) as Record<string, unknown>) : undefined,
      };
    })
    .filter((call) => call.method !== "GET");

const date = (value: string) => formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });

/** The secret's own input: the Entra credential choice beside it is labelled "Client secret" too. */
const secretInput = () => screen.getByLabelText("Client secret", { selector: "input[type=password]" });

const type = (label: string, value: string) =>
  fireEvent.change(label === "Client secret" ? secretInput() : screen.getByLabelText(label), { target: { value } });

describe("the Single sign-on card", () => {
  it("renders the row: Entra on with its stored secret, SCIM on with an overlap, the import notice", async () => {
    await renderCard();

    expect(screen.getByText("Single sign-on")).toBeInTheDocument();
    expect(screen.getByText("Imported from the environment")).toBeInTheDocument();
    expect(screen.getByText(/the variables are ignored: remove them/)).toBeInTheDocument();
    expect(screen.getByDisplayValue("Microsoft Entra ID")).toBeInTheDocument();
    expect(screen.getByLabelText("Authority")).toHaveValue(authority);
    expect(screen.getByLabelText("Client ID")).toHaveValue("22222222-2222-2222-2222-222222222222");
    expect(secretInput()).toHaveValue("");
    expect(screen.getByText(`Set on ${date("2026-10-01T08:00:00Z")}. Leave it blank to keep it.`)).toBeInTheDocument();
    expect(screen.getByLabelText("Display name")).toHaveValue("Contoso SSO");
    expect(screen.getByText(date("2026-10-19T09:30:00Z"))).toBeInTheDocument();
    expect(screen.getByDisplayValue("https://vantigo.example.com/api/v1/identity/scim/v2")).toBeInTheDocument();
    expect(screen.getByText(date("2026-10-18T08:00:00Z"))).toBeInTheDocument();
    expect(
      screen.getByText(`The previous token is accepted until ${date("2026-10-21T08:00:00Z")}.`),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "End overlap" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("saves with If-Match on the version it read, no secret while the field is blank, and the answer's version next", async () => {
    let version = 7;
    const fetchMock = await renderCard((method, url, init) => {
      if (method !== "PUT" || url !== "/api/v1/identity/owner/sso/oidc") return undefined;
      const body = JSON.parse(String(init?.body)) as { displayName?: string };
      version += 1;
      const answer = entraWithOverlap();
      return json({ ...answer, version, oidc: { ...answer.oidc, displayName: body.displayName } });
    });

    type("Display name", "Contoso sign-in");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    const [put] = writes(fetchMock);
    expect(put.headers["if-match"]).toBe("7");
    expect(put.body).toEqual({
      provider: "entra",
      authority,
      clientId: "22222222-2222-2222-2222-222222222222",
      displayName: "Contoso sign-in",
    });
    expect(put.body).not.toHaveProperty("clientSecret");
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeDisabled());

    type("Display name", "Contoso");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(writes(fetchMock)[1].headers["if-match"]).toBe("8");
  });

  it("sends a typed secret and shows a field error from a 400 beside its field", async () => {
    const message = "The authority must be https://login.microsoftonline.com/<tenant-guid>/v2.0.";
    const fetchMock = await renderCard((method) =>
      method === "PUT" ? refusal(400, "validation_failed", { authority: [message] }) : undefined,
    );

    type("Authority", "https://login.microsoftonline.com/common/v2.0");
    type("Client secret", "s3cret");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.getByLabelText("Authority")).toHaveAttribute("aria-invalid", "true");
    expect(writes(fetchMock)[0].body).toMatchObject({ clientSecret: "s3cret" });
  });

  it("asks before changing the authority while people are linked, then sends confirmIssuerChange", async () => {
    const fetchMock = await renderCard((method, _url, init) => {
      if (method !== "PUT") return undefined;
      const body = JSON.parse(String(init?.body)) as { confirmIssuerChange?: boolean };
      if (!body.confirmIssuerChange) return refusal(409, "oidc_links_exist");
      const answer = entraWithOverlap();
      return json({ ...answer, version: 8, oidc: { ...answer.oidc, authority: otherAuthority } });
    });

    type("Authority", otherAuthority);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const dialog = await screen.findByRole("dialog", { name: "Change the provider's authority?" });
    expect(
      within(dialog).getByText(new RegExp(`linked to the provider under ${quote(authority)}`)),
    ).toBeInTheDocument();
    expect(writes(fetchMock)).toHaveLength(1);
    expect(writes(fetchMock)[0].body).not.toHaveProperty("confirmIssuerChange");

    fireEvent.click(within(dialog).getByRole("button", { name: "Change authority" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(writes(fetchMock)[1].body).toMatchObject({ authority: otherAuthority, confirmIssuerChange: true });
    expect(writes(fetchMock)[1].headers["if-match"]).toBe("7");
    await waitFor(() => expect(screen.getByLabelText("Authority")).toHaveValue(otherAuthority));
  });

  it("turns OIDC off with confirm=true only after the server says people are linked", async () => {
    const fetchMock = await renderCard((method, url) => {
      if (method !== "DELETE" || !url.startsWith("/api/v1/identity/owner/sso/oidc")) return undefined;
      if (!url.endsWith("?confirm=true")) return refusal(409, "oidc_links_exist");
      const answer = entraWithOverlap();
      return json({
        ...answer,
        version: 8,
        oidc: { enabled: false, allowedDomains: [], callbackUrl: answer.oidc.callbackUrl },
      });
    });

    fireEvent.click(screen.getAllByRole("button", { name: "Turn off" })[0]);
    const first = await screen.findByRole("dialog", { name: "Turn off SSO?" });
    fireEvent.click(within(first).getByRole("button", { name: "Turn off" }));

    const second = await screen.findByRole("dialog", { name: "People are linked to this provider" });
    expect(writes(fetchMock).map((call) => call.url)).toEqual(["/api/v1/identity/owner/sso/oidc"]);
    fireEvent.click(within(second).getByRole("button", { name: "Turn off" }));

    await waitFor(() =>
      expect(writes(fetchMock).map((call) => call.url)).toEqual([
        "/api/v1/identity/owner/sso/oidc",
        "/api/v1/identity/owner/sso/oidc?confirm=true",
      ]),
    );
    await waitFor(() => expect(screen.getByDisplayValue("Off")).toBeInTheDocument());
    expect(screen.queryByLabelText("Authority")).not.toBeInTheDocument();
  });

  it("shows a new token once, then the new tokenSetAt from the answer's settings", async () => {
    const token = "Zm9vYmFyLXRoZS1uZXctdG9rZW4tb25jZS1hbmQtbm8tbW9yZQ";
    const fetchMock = await renderCard((method, url) => {
      if (method !== "POST" || url !== "/api/v1/identity/owner/sso/scim/token") return undefined;
      const answer = entraWithOverlap();
      const scim = { ...answer.scim, tokenSetAt: "2026-10-20T12:00:00Z", previousExpiresAt: "2026-10-21T12:00:00Z" };
      return json({
        token,
        endpointUrl: answer.scim.endpointUrl,
        previousExpiresAt: scim.previousExpiresAt,
        settings: { ...answer, version: 8, scim },
      });
    });

    fireEvent.click(screen.getByRole("button", { name: "New token" }));
    const ask = await screen.findByRole("dialog", { name: "Issue a new token?" });
    expect(within(ask).getByLabelText("Accept the current token for (hours)")).toHaveValue("24");
    fireEvent.click(within(ask).getByRole("button", { name: "Issue token" }));

    const shown = await screen.findByRole("dialog", { name: "The new SCIM token" });
    expect(within(shown).getByText(token)).toBeInTheDocument();
    expect(within(shown).getByText(/It will not be shown again\./)).toBeInTheDocument();
    expect(within(shown).getByRole("button", { name: "Copy token" })).toBeInTheDocument();
    expect(writes(fetchMock)).toEqual([expect.objectContaining({ method: "POST", body: {} })]);

    fireEvent.click(within(shown).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByText(token)).not.toBeInTheDocument());
    expect(screen.getByText(date("2026-10-20T12:00:00Z"))).toBeInTheDocument();
    expect(
      screen.getByText(`The previous token is accepted until ${date("2026-10-21T12:00:00Z")}.`),
    ).toBeInTheDocument();
  });

  it("says to reload on a 412", async () => {
    await renderCard((method) => (method === "PUT" ? refusal(412, "version_mismatch") : undefined));

    type("Display name", "Contoso sign-in");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("The settings have changed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
  });
});
