import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  connected,
  type GoogleServer,
  KEY_ID,
  key,
  keyFile,
  NUMERIC_CLIENT_ID,
  PRIVATE_KEY,
  REDIRECT_URIS,
  SCOPES,
  SERVICE_ACCOUNT,
  stubGoogleApi,
  unconfigured,
  WEB_CLIENT_ID,
} from "../test/server";
import { GoogleConnectionTab } from "./connection-tab";

afterEach(() => setLanguagePreference("auto"));

/** The Connection tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (
  server: GoogleServer = {},
  firstStep = "Create the project, the service account and its key",
) => {
  const fetchMock = stubGoogleApi(server);
  const user = userEvent.setup();
  renderWithProviders(<GoogleConnectionTab />);
  await screen.findByRole("region", { name: firstStep });
  return { fetchMock, user };
};

const region = (name: string) => screen.getByRole("region", { name });

describe("GoogleConnectionTab — the steps", () => {
  it("shows the six steps in order, with the org-policy note and the guide", async () => {
    await openPage();
    const titles = screen.getAllByRole("heading", { level: 3 }).map((heading) => heading.textContent);
    expect(titles).toEqual([
      "Create the project, the service account and its key",
      "Upload the key",
      "Delegate the scopes",
      "Name the admin to impersonate",
      "Test the connection",
      "Set up the Internal web client",
    ]);
    const first = region("Create the project, the service account and its key");
    expect(within(first).getByText(/iam\.disableServiceAccountKeyCreation/)).toBeInTheDocument();
    expect(within(first).getByRole("link", { name: "Read the guide to connecting Google Workspace" })).toHaveAttribute(
      "href",
      "/docs/en/admin/google-workspace/",
    );
  });

  it("says every step in Norwegian", async () => {
    setLanguagePreference("nb");
    await openPage({}, "Opprett prosjektet, tjenestekontoen og nøkkelen");
    for (const title of [
      "Last opp nøkkelen",
      "Deleger tilgangsområdene",
      "Oppgi administratoren som skal etterlignes",
      "Test tilkoblingen",
      "Sett opp den interne nettklienten",
    ])
      expect(region(title)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Last opp nøkkel" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Les veiledningen for å koble til Google Workspace" })).toHaveAttribute(
      "href",
      "/docs/nb/admin/google-workspace/",
    );
  });
});

describe("GoogleConnectionTab — the key", () => {
  it("posts a pasted key as the file's text and shows only its four clear fields back", async () => {
    const { fetchMock, user } = await openPage();
    const step = region("Upload the key");
    const text = keyFile();
    await user.click(within(step).getByRole("textbox", { name: "The key file's contents" }));
    await user.paste(text);
    await user.click(within(step).getByRole("button", { name: "Upload key" }));

    const table = await within(step).findByRole("table", { name: "Service account keys" });
    expect(sent(fetchMock, "POST")).toMatchObject({
      url: "/api/v1/google/connection/credentials",
      body: { json: text },
    });
    expect(within(table).getByText(SERVICE_ACCOUNT)).toBeInTheDocument();
    expect(within(table).getByText(NUMERIC_CLIENT_ID)).toBeInTheDocument();
    expect(within(table).getByText(KEY_ID)).toBeInTheDocument();
    expect(within(table).getByText("Nov 3, 2026")).toBeInTheDocument();
    // The key never comes back: not in the table, not in the emptied text area.
    expect(within(step).getByRole("textbox", { name: "The key file's contents" })).toHaveValue("");
    expect(document.body.textContent).not.toContain("BEGIN PRIVATE KEY");
    expect(document.body.innerHTML).not.toContain(PRIVATE_KEY.slice(0, 40));
  });

  it("reads a chosen .json file in the browser and posts its text", async () => {
    const { fetchMock, user } = await openPage();
    const step = region("Upload the key");
    const text = keyFile();
    const input = step.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    expect(input).toHaveAttribute("accept", ".json,application/json");
    await user.upload(
      input as HTMLInputElement,
      new File([text], "contoso-vantigo.json", { type: "application/json" }),
    );

    await within(step).findByRole("table", { name: "Service account keys" });
    expect(sent(fetchMock, "POST").body).toEqual({ json: text });
    expect(document.body.textContent).not.toContain("BEGIN PRIVATE KEY");
  });

  it("says a key for another service account is refused, in words", async () => {
    const { user } = await openPage({ connection: unconfigured({ credentials: [key()] }) });
    const step = region("Upload the key");
    await user.click(within(step).getByRole("textbox", { name: "The key file's contents" }));
    await user.paste(keyFile({ client_id: "999" }));
    await user.click(within(step).getByRole("button", { name: "Upload key" }));

    expect(
      await within(step).findByText(
        "This key is for another service account. Revoke every key of the current one first: delegation is granted to a client id.",
      ),
    ).toBeInTheDocument();
  });

  it("shows the server's field error on the key", async () => {
    const { user } = await openPage();
    const step = region("Upload the key");
    await user.click(within(step).getByRole("textbox", { name: "The key file's contents" }));
    await user.paste(keyFile({ type: "authorized_user" }));
    await user.click(within(step).getByRole("button", { name: "Upload key" }));
    expect(await within(step).findByText("The key is not a service account's.")).toBeInTheDocument();
  });

  it("says a key was refused only while it has not worked since", async () => {
    await openPage({
      connection: connected({
        credentials: [
          key({ id: 1, lastFailedAt: "2026-11-03T09:00:00Z", lastUsedAt: "2026-11-03T08:00:00Z" }),
          key({
            id: 3,
            privateKeyId: "b".repeat(40),
            lastFailedAt: "2026-11-02T09:00:00Z",
            lastUsedAt: "2026-11-03T10:00:00Z",
          }),
          key({ id: 4, privateKeyId: "c".repeat(40), lastFailedAt: "2026-11-01T09:00:00Z" }),
        ],
      }),
    });
    const table = within(region("Upload the key")).getByRole("table", { name: "Service account keys" });
    expect(within(table).getByText("Google refused it Nov 3, 2026")).toBeInTheDocument();
    expect(within(table).getByText("Google refused it Nov 1, 2026")).toBeInTheDocument();
    expect(within(table).queryByText("Google refused it Nov 2, 2026")).not.toBeInTheDocument();
  });

  it("revokes a key after asking", async () => {
    const { fetchMock, user } = await openPage({ connection: connected() });
    const table = within(region("Upload the key")).getByRole("table", { name: "Service account keys" });
    await user.click(within(table).getByRole("button", { name: `Revoke key ${KEY_ID}` }));
    const dialog = await screen.findByRole("dialog", { name: `Revoke key ${KEY_ID}?` });
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "DELETE").map((call) => call.url)).toEqual(["/api/v1/google/connection/credentials/1"]),
    );
    expect(await within(region("Upload the key")).findByText("Revoked Nov 3, 2026")).toBeInTheDocument();
  });
});

describe("GoogleConnectionTab — delegation, the admin and the test", () => {
  it("offers the numeric client id and the scopes, comma-separated, to copy", async () => {
    const { user } = await openPage({ connection: connected() });
    const step = region("Delegate the scopes");
    expect(within(step).getByText(/Security › Access and data control › API controls › Domain-wide delegation/));
    expect(within(step).getByText(SCOPES.join(","))).toBeInTheDocument();

    await user.click(within(step).getByRole("button", { name: "Copy the scopes" }));
    expect(await navigator.clipboard.readText()).toBe(SCOPES.join(","));
    await user.click(within(step).getByRole("button", { name: `Copy ${NUMERIC_CLIENT_ID}` }));
    expect(await navigator.clipboard.readText()).toBe(NUMERIC_CLIENT_ID);
  });

  it("saves the admin under If-Match and says a version mismatch", async () => {
    const { fetchMock, user } = await openPage({ connection: unconfigured({ credentials: [key()], version: 3 }) });
    const step = region("Name the admin to impersonate");
    await user.type(within(step).getByRole("textbox", { name: "Admin's address" }), "admin@contoso.no");
    await user.click(within(step).getByRole("button", { name: "Save" }));

    await screen.findByText("Admin saved");
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/google/connection",
      headers: { "if-match": "3" },
      body: { adminSubject: "admin@contoso.no" },
    });

    fetchMock.state.connection = { ...fetchMock.state.connection, version: 9 };
    const field = within(step).getByRole("textbox", { name: "Admin's address" });
    await user.clear(field);
    await user.type(field, "other@contoso.no");
    await user.click(within(step).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Somebody else saved the connection; it has been reloaded")).toBeInTheDocument();
  });

  it("tests the connection and shows what Google answered", async () => {
    const { fetchMock, user } = await openPage({
      connection: unconfigured({ credentials: [key()], adminSubject: "admin@contoso.no" }),
      test: {
        status: "connected",
        customer: { id: "C01abc234", organisationName: "Contoso AS", primaryDomain: "contoso.no" },
        domains: ["contoso.com", "contoso.no"],
        granted: [SCOPES[0]],
        missing: [SCOPES[1]],
        unexpected: ["https://www.googleapis.com/auth/drive"],
      },
    });
    const step = region("Test the connection");
    await user.click(within(step).getByRole("button", { name: "Test connection" }));

    const result = await within(step).findByRole("region", { name: "Test result" });
    expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual(["/api/v1/google/connection/test"]);
    expect(within(result).getByText("Connected")).toBeInTheDocument();
    expect(within(result).getByText("Contoso AS")).toBeInTheDocument();
    expect(within(result).getByText("C01abc234")).toBeInTheDocument();
    expect(within(result).getByText("contoso.com, contoso.no")).toBeInTheDocument();
    expect(within(within(result).getByRole("list", { name: "Missing scopes" })).getByText(SCOPES[1])).toBeVisible();
    expect(
      within(within(result).getByRole("list", { name: "Scopes Vantigo does not use" })).getByText(
        "https://www.googleapis.com/auth/drive",
      ),
    ).toBeVisible();
  });

  it("says the pending delegation's reason after a test", async () => {
    const { user } = await openPage({
      connection: unconfigured({ credentials: [key()], adminSubject: "admin@contoso.no" }),
      test: {
        status: "pending_delegation",
        reason: "not_delegated",
        domains: [],
        granted: [],
        missing: [...SCOPES],
        unexpected: [],
      },
    });
    const step = region("Test the connection");
    await user.click(within(step).getByRole("button", { name: "Test connection" }));
    const result = await within(step).findByRole("region", { name: "Test result" });
    expect(within(result).getByText("Waiting for delegation")).toBeInTheDocument();
    expect(within(result).getByText(/Domain-wide delegation can take up to 24 hours to apply/)).toBeInTheDocument();
  });
});

describe("GoogleConnectionTab — the web client", () => {
  it("lists both redirect URIs to copy and says the secret is shown once", async () => {
    const { user } = await openPage();
    const step = region("Set up the Internal web client");
    for (const uri of REDIRECT_URIS) expect(within(step).getByText(uri)).toBeInTheDocument();
    await user.click(within(step).getByRole("button", { name: `Copy ${REDIRECT_URIS[1]}` }));
    expect(await navigator.clipboard.readText()).toBe(REDIRECT_URIS[1]);
    expect(within(step).getByText(/Google shows a client secret only once/)).toBeInTheDocument();
    expect(within(step).getByText(/Internal/, { selector: "p" })).toBeInTheDocument();
  });

  it("sends the id and the secret, then shows the id and the date, never the secret", async () => {
    const { fetchMock, user } = await openPage();
    const step = region("Set up the Internal web client");
    await user.type(within(step).getByRole("textbox", { name: "Client ID" }), WEB_CLIENT_ID);
    await user.type(within(step).getByLabelText("Client secret"), "GOCSPX-very-secret");
    await user.click(within(step).getByRole("button", { name: "Save" }));

    expect(await within(step).findByText("Secret added on Nov 3, 2026")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/google/connection/oauth-client",
      body: { clientId: WEB_CLIENT_ID, clientSecret: "GOCSPX-very-secret" },
    });
    expect(within(step).getByText(`Web client: ${WEB_CLIENT_ID}`)).toBeInTheDocument();
    expect(within(step).getByLabelText("Client secret")).toHaveValue("");
    expect(document.body.innerHTML).not.toContain("GOCSPX-very-secret");
  });

  it("refuses a client id that is not a web client's", async () => {
    const { fetchMock, user } = await openPage();
    const step = region("Set up the Internal web client");
    await user.type(within(step).getByRole("textbox", { name: "Client ID" }), "not-a-client");
    await user.click(within(step).getByRole("button", { name: "Save" }));
    expect(
      await within(step).findByText("A web client's ID ends with .apps.googleusercontent.com."),
    ).toBeInTheDocument();
    expect(sentAll(fetchMock, "PUT")).toEqual([]);
  });
});

describe("GoogleConnectionTab — who may write", () => {
  it("shows a manager who is not an Owner the steps read-only", async () => {
    await openPage({ connection: connected(), owner: false });
    expect(screen.queryByRole("form", { name: "Upload a key" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Admin's address" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Test connection" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Revoke/ })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Client secret")).not.toBeInTheDocument();
    expect(screen.getAllByText("An Owner does this step. You can see what is set, but not change it.")).toHaveLength(4);
    expect(screen.getByText("Admin to impersonate: admin@contoso.no")).toBeInTheDocument();
    expect(screen.getByText(SERVICE_ACCOUNT)).toBeInTheDocument();
  });

  it("offers an Owner every form", async () => {
    await openPage({ connection: connected() });
    expect(screen.getByRole("form", { name: "Upload a key" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Admin's address" })).toHaveValue("admin@contoso.no");
    expect(screen.getByRole("button", { name: "Test connection" })).toBeEnabled();
    expect(screen.queryByText("An Owner does this step. You can see what is set, but not change it.")).toBeNull();
  });
});
