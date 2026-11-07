import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  type MetaServer,
  NEW_VERIFY_TOKEN,
  page,
  settings,
  stubMetaApi,
  VERIFY_TOKEN,
  WEBHOOK_URL,
} from "../test/server";
import { MetaSettingsPage } from "./settings-page";

afterEach(() => setLanguagePreference("auto"));

const openPage = async (server: MetaServer = {}) => {
  const fetchMock = stubMetaApi(server);
  renderWithProviders(<MetaSettingsPage />);
  await screen.findByRole("region", { name: "Meta app" });
  return { fetchMock };
};

const stubClipboard = () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
  return writeText;
};

describe("MetaSettingsPage", () => {
  it("shows the app, the webhook, the Pages and the status", async () => {
    await openPage({
      settings: settings({
        appId: "1234567890",
        appSecretSet: true,
        pages: [page()],
        status: { lastWebhookAt: "2026-11-06T08:30:00Z" },
      }),
    });
    expect(screen.getByLabelText("App ID")).toHaveValue("1234567890");
    expect(screen.getByText("The app secret is set. Leave the field empty to keep it.")).toBeInTheDocument();
    expect(screen.getByLabelText("App secret")).toHaveValue("");
    expect(screen.getByText(WEBHOOK_URL)).toBeInTheDocument();
    expect(screen.getByText(VERIFY_TOKEN)).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Pages" })).getByText("Contoso Støtte")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Status" })).getByText(/^Nov 6, 2026/)).toBeInTheDocument();
    expect(screen.getByText(/system user's token, which does not expire, is recommended/)).toBeInTheDocument();
  });

  it("saves the app id and secret, and never holds the secret afterwards", async () => {
    const { fetchMock } = await openPage();
    await userEvent.type(screen.getByLabelText("App ID"), "1234567890");
    await userEvent.type(screen.getByLabelText("App secret"), "s3cr3t");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "PUT").body).toEqual({
        appId: "1234567890",
        appSecret: "s3cr3t",
        humanAgentGranted: false,
      }),
    );
    expect(sent(fetchMock, "PUT").url).toBe("/api/v1/meta/settings");
    expect(await screen.findByText("The Meta settings are saved.")).toBeInTheDocument();
    expect(await screen.findByText("The app secret is set. Leave the field empty to keep it.")).toBeInTheDocument();
    expect(screen.getByLabelText("App secret")).toHaveValue("");
  });

  it("keeps the stored secret when the field is left empty", async () => {
    const { fetchMock } = await openPage({ settings: settings({ appId: "42", appSecretSet: true }) });
    await userEvent.click(screen.getByLabelText("Meta has granted this app the Human Agent permission"));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toEqual({ appId: "42", humanAgentGranted: true });
  });

  it("copies the webhook address and the verify token", async () => {
    const writeText = stubClipboard();
    await openPage();
    await userEvent.click(screen.getByRole("button", { name: "Copy Callback URL" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(WEBHOOK_URL));
    await userEvent.click(screen.getByRole("button", { name: "Copy Verify token" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(VERIFY_TOKEN));
  });

  it("regenerates the verify token after a confirmation", async () => {
    const { fetchMock } = await openPage();
    await userEvent.click(screen.getByRole("button", { name: "Regenerate" }));
    const dialog = await screen.findByRole("dialog");
    expect(sentAll(fetchMock, "POST")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Regenerate" }));

    expect(await screen.findByText(NEW_VERIFY_TOKEN)).toBeInTheDocument();
    expect(sent(fetchMock, "POST").url).toBe("/api/v1/meta/settings/verify-token");
    expect(screen.queryByText(VERIFY_TOKEN)).not.toBeInTheDocument();
  });

  it("adds a Page by its access token and empties the field", async () => {
    const { fetchMock } = await openPage({ settings: settings({ appId: "42", appSecretSet: true }) });
    await userEvent.type(screen.getByLabelText("Access token"), "EAAB-token");
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));

    expect(await within(screen.getByRole("region", { name: "Pages" })).findByText("Contoso Salg")).toBeInTheDocument();
    expect(sent(fetchMock, "POST")).toMatchObject({ url: "/api/v1/meta/pages", body: { accessToken: "EAAB-token" } });
    expect(screen.getByLabelText("Access token")).toHaveValue("");
  });

  it.each([
    [422, "page_token_insufficient", "The token is neither a Page's access token nor a user's or system user's token"],
    [422, "not_configured", "Save the app ID and app secret first, then add the Page."],
    [503, "meta_unreachable", "Meta did not answer. Try again in a while."],
  ])("says %s %s in words when adding a Page", async (status, code, sentence) => {
    await openPage({ addPage: () => problemResponse(status, "refused", { code }) });
    await userEvent.type(screen.getByLabelText("Access token"), "EAAB-token");
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));

    expect(await screen.findByText(new RegExp(`^${sentence}`))).toBeInTheDocument();
    expect(screen.getByLabelText("Access token")).toHaveValue("EAAB-token");
  });

  it("lists the Pages of a token that manages several, and connects the chosen one", async () => {
    let calls = 0;
    const { fetchMock } = await openPage({
      addPage: () => {
        calls += 1;
        return calls === 1
          ? problemResponse(422, "choose", {
              code: "page_choice_required",
              choices: [
                { id: "111", name: "Contoso Salg" },
                { id: "222", name: "Contoso Støtte" },
              ],
            })
          : jsonResponse(201, page({ name: "Contoso Støtte", pageId: "222" }));
      },
    });
    await userEvent.type(screen.getByLabelText("Access token"), "EAAB-system");
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));

    expect(await screen.findByText("Choose the Page to connect")).toBeInTheDocument();
    expect(screen.getByLabelText(/Contoso Salg/)).toBeInTheDocument();
    expect(screen.getByText("Page ID 222")).toBeInTheDocument();
    expect(screen.getByLabelText("Access token")).toHaveValue("EAAB-system");

    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));
    expect(sentAll(fetchMock, "POST")).toHaveLength(1);

    await userEvent.click(screen.getByLabelText(/Contoso Støtte/));
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));

    await waitFor(() => expect(sentAll(fetchMock, "POST")).toHaveLength(2));
    expect(sentAll(fetchMock, "POST")[1]).toMatchObject({
      url: "/api/v1/meta/pages",
      body: { accessToken: "EAAB-system", pageId: "222" },
    });
    await waitFor(() => expect(screen.queryByText("Choose the Page to connect")).not.toBeInTheDocument());
    expect(screen.getByLabelText("Access token")).toHaveValue("");
  });

  it("drops the choice when the token is edited", async () => {
    await openPage({
      addPage: () =>
        problemResponse(422, "choose", {
          code: "page_choice_required",
          choices: [{ id: "111", name: "Contoso Salg" }],
        }),
    });
    await userEvent.type(screen.getByLabelText("Access token"), "EAAB-a");
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));
    expect(await screen.findByText("Choose the Page to connect")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Access token"), "b");
    expect(screen.queryByText("Choose the Page to connect")).not.toBeInTheDocument();
  });

  it("asks for a token before it calls the server", async () => {
    const { fetchMock } = await openPage();
    await userEvent.click(screen.getByRole("button", { name: "Add Page" }));
    expect(await screen.findByText("Enter the access token.")).toBeInTheDocument();
    expect(sentAll(fetchMock, "POST")).toHaveLength(0);
  });

  it("removes a Page after a confirmation", async () => {
    const { fetchMock } = await openPage({ settings: settings({ pages: [page()] }) });
    await userEvent.click(screen.getByRole("button", { name: "Remove Contoso Støtte" }));
    const dialog = await screen.findByRole("dialog");
    expect(sentAll(fetchMock, "DELETE")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(screen.getByText("No Page is connected yet.")).toBeInTheDocument());
    expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/meta/pages/104200000000001");
  });

  it("re-subscribes a Page without asking for its token", async () => {
    const { fetchMock } = await openPage({ settings: settings({ pages: [page()] }) });
    await userEvent.click(screen.getByRole("button", { name: "Re-subscribe Contoso Støtte" }));
    expect(await screen.findByText("Page re-subscribed")).toBeInTheDocument();
    expect(sent(fetchMock, "POST").url).toBe("/api/v1/meta/pages/104200000000001/resubscribe");
  });

  it("says that a Page could not be re-subscribed", async () => {
    await openPage({
      settings: settings({ pages: [page()] }),
      resubscribe: () => problemResponse(422, "page_token_insufficient"),
    });
    await userEvent.click(screen.getByRole("button", { name: "Re-subscribe Contoso Støtte" }));
    expect(await screen.findByText("The Page could not be re-subscribed")).toBeInTheDocument();
  });

  it("tells the Owner to add a Page again when Meta rejected its token", async () => {
    await openPage({ settings: settings({ pages: [page({ state: "token_rejected" })] }) });
    expect(screen.getByText("Token rejected")).toBeInTheDocument();
    expect(screen.getByText(/Add the Page again with a new token/)).toBeInTheDocument();
  });

  it("explains a missing public host and does not let the settings be saved", async () => {
    await openPage({ settings: settings({ publicHostConfigured: false, webhookUrl: null }) });
    expect(screen.getByRole("alert")).toHaveTextContent("This instance has no public host");
    expect(screen.getByRole("alert")).toHaveTextContent("PUBLIC_BASE_URL");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByText("The address is known once the instance has a public host.")).toBeInTheDocument();
  });

  it("tells the Owner to clear Human Agent when Meta refused a reply for it", async () => {
    await openPage({
      settings: settings({
        humanAgentGranted: true,
        status: {
          lastGraphErrorCode: "human_agent_not_permitted",
          lastGraphErrorMessage: "(#10) Application does not have permission",
          lastGraphErrorAt: "2026-11-06T08:30:00Z",
        },
      }),
    });
    expect(screen.getByText(/Clear the Human Agent checkbox above and save/)).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Status" })).getByText(/The Human Agent permission is not granted/),
    ).toBeInTheDocument();
  });

  it("does not show the hint for other Graph errors", async () => {
    await openPage({
      settings: settings({ status: { lastGraphErrorCode: "190", lastGraphErrorMessage: "Invalid token" } }),
    });
    expect(screen.queryByText(/Clear the Human Agent checkbox/)).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Status" })).getByText(/190: the Page's access token was rejected/),
    ).toBeInTheDocument();
  });

  it("shows the public-host refusal of a save in words", async () => {
    await openPage({ put: () => problemResponse(422, "refused", { code: "public_host_required" }) });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(/The instance has no public host/)).toBeInTheDocument();
  });

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    stubMetaApi({ settings: settings({ pages: [page()] }) });
    renderWithProviders(<MetaSettingsPage />);
    expect(await screen.findByRole("region", { name: "Meta-app" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Legg til side" })).toBeInTheDocument();
  });
});
