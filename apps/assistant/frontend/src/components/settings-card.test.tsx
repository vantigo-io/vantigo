import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { permissions, settings, stubAssistantApi } from "../test/server";
import { AssistantSettingsCard } from "./settings-card";
import "../i18n";

const puts = (fetchMock: ReturnType<typeof stubAssistantApi>) => sentAll(fetchMock, "PUT");

describe("AssistantSettingsCard: who has the assistant", () => {
  it("shows the three modes and saves a choice under the settings' version", async () => {
    const fetchMock = stubAssistantApi({ settings: settings({ mode: "all", version: 3 }) });
    renderWithProviders(<AssistantSettingsCard />);

    expect(await screen.findByRole("radio", { name: /Enabled for all/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /Per person/ })).not.toBeChecked();
    expect(screen.getByRole("radio", { name: /Disabled for all/ })).not.toBeChecked();

    await userEvent.click(screen.getByRole("radio", { name: /Per person/ }));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Per person/ })).toBeChecked());
    expect(puts(fetchMock)[0]).toMatchObject({ url: "/api/v1/assistant/settings", body: { mode: "per_person" } });
    expect(puts(fetchMock)[0].headers["if-match"]).toBe("3");

    await userEvent.click(screen.getByRole("radio", { name: /Disabled for all/ }));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Disabled for all/ })).toBeChecked());
    // The next save names the version the last one returned.
    expect(puts(fetchMock)[1].body).toEqual({ mode: "disabled" });
    expect(puts(fetchMock)[1].headers["if-match"]).toBe("4");
  });

  it("saves the retention between 1 and 60 months", async () => {
    const fetchMock = stubAssistantApi({ settings: settings({ retentionMonths: 12 }) });
    renderWithProviders(<AssistantSettingsCard />);
    const input = await screen.findByRole("textbox", { name: /Keep conversations for/ });
    const save = screen.getAllByRole("button", { name: "Save" })[0];
    expect(save).toBeDisabled();

    await userEvent.clear(input);
    await userEvent.type(input, "99");
    await userEvent.tab();
    // Over the limit it is clamped, never saved as 99.
    await waitFor(() => expect(input).toHaveValue("60 months"));
    await userEvent.clear(input);
    await userEvent.type(input, "24");
    await userEvent.click(save);
    await waitFor(() => expect(puts(fetchMock)[0]?.body).toEqual({ retentionMonths: 24 }));
  });

  it("names the provider when the data leaves the installation, and says nothing leaves when it is local", async () => {
    stubAssistantApi({ settings: settings({ local: false, provider: "OpenAI" }) });
    const first = renderWithProviders(<AssistantSettingsCard />);
    await screen.findByRole("radio", { name: /Enabled for all/ });
    const notices = screen.getAllByTestId("data-notice");
    expect(notices.length).toBeGreaterThanOrEqual(2);
    expect(notices[0]).toHaveTextContent("Questions and the data the assistant reads are sent to OpenAI.");
    first.unmount();

    stubAssistantApi({ settings: settings({ local: true }) });
    renderWithProviders(<AssistantSettingsCard />);
    await screen.findByRole("radio", { name: /Enabled for all/ });
    expect(screen.getAllByTestId("data-notice")[0]).toHaveTextContent("The model runs on this installation");
  });

  it("says there is no provider when the instance has none", async () => {
    stubAssistantApi({ settings: settings({ configured: false }) });
    renderWithProviders(<AssistantSettingsCard />);
    expect(await screen.findByText(/no AI provider/)).toBeInTheDocument();
  });

  it("reloads and says so when somebody else saved first", async () => {
    const fetchMock = stubAssistantApi({
      settings: settings({ version: 3 }),
      handle: (method, path) =>
        method === "PUT" && path === "/api/v1/assistant/settings"
          ? new Response(JSON.stringify({ title: "stale", status: 409, code: "version_conflict" }), {
              status: 409,
              headers: { "Content-Type": "application/problem+json" },
            })
          : undefined,
    });
    renderWithProviders(<AssistantSettingsCard />);
    await userEvent.click(await screen.findByRole("radio", { name: /Disabled for all/ }));
    expect(await screen.findByText("Somebody else saved these settings")).toBeInTheDocument();
    const reads = () =>
      fetchMock.actualCalls.filter(([url, init]) => String(url) === "/api/v1/assistant/settings" && !init?.method)
        .length;
    await waitFor(() => expect(reads()).toBeGreaterThan(1));
  });
});

describe("AssistantSettingsCard: what the assistant may use", () => {
  it("sets the sensitive data switch and reloads the permissions, whose defaults it moves", async () => {
    const fetchMock = stubAssistantApi();
    renderWithProviders(<AssistantSettingsCard />);
    expect(await screen.findByRole("radio", { name: "Not available" })).toBeChecked();
    await userEvent.click(screen.getByRole("radio", { name: "Can read" }));
    await waitFor(() => expect(puts(fetchMock)[0]?.body).toEqual({ sensitiveDefault: "read" }));
    await waitFor(() => expect(screen.getByRole("radio", { name: "Can read" })).toBeChecked());
    const listReads = fetchMock.actualCalls.filter(
      ([url]) => String(url) === "/api/v1/assistant/settings/permissions",
    ).length;
    await waitFor(() => expect(listReads).toBeGreaterThanOrEqual(1));
  });

  it("groups the catalog by module and category, marks sensitive and changed permissions", async () => {
    stubAssistantApi();
    renderWithProviders(<AssistantSettingsCard />);
    const view = await screen.findByText("View customers");
    const row = view.closest("tr");
    expect(row).not.toBeNull();
    expect(screen.getByText(/customers\s*›\s*Customers/)).toBeInTheDocument();
    expect(screen.getByText(/invoices\s*›\s*Invoices/)).toBeInTheDocument();

    const contacts = screen.getByText("View contacts").closest("tr") as HTMLElement;
    expect(within(contacts).getByText("Sensitive")).toBeInTheDocument();
    const invoices = screen.getByText("Read invoices").closest("tr") as HTMLElement;
    expect(within(invoices).getByText("Changed")).toBeInTheDocument();
  });

  it("offers Off, Read and Read and change per permission, marking the last as coming later", async () => {
    const fetchMock = stubAssistantApi();
    renderWithProviders(<AssistantSettingsCard />);
    const select = await screen.findByRole("combobox", { name: "Level for View customers" });
    expect(select).toHaveValue("Read and change");
    await userEvent.click(select);
    expect(screen.getByRole("option", { name: "Off" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Read" })).toBeInTheDocument();
    const change = screen.getByRole("option", { name: /Read and change/ });
    expect(change).toHaveTextContent("Changes come in a later version");

    await userEvent.click(screen.getByRole("option", { name: "Read" }));
    await waitFor(() =>
      expect(puts(fetchMock).find((call) => call.url.endsWith("customers%3Aview"))?.body).toEqual({ level: "read" }),
    );
    const call = puts(fetchMock).find((one) => one.url.endsWith("customers%3Aview"));
    expect(call?.headers["if-match"]).toBe("3");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Level for View customers" })).toHaveValue("Read"));
  });

  it("filters to the sensitive and the overridden permissions", async () => {
    stubAssistantApi();
    renderWithProviders(<AssistantSettingsCard />);
    await screen.findByText("View customers");

    await userEvent.click(screen.getByRole("radio", { name: "Sensitive" }));
    expect(screen.getByText("View contacts")).toBeInTheDocument();
    expect(screen.queryByText("View customers")).not.toBeInTheDocument();
    expect(screen.queryByText("Read invoices")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Overridden" }));
    expect(screen.getByText("Read invoices")).toBeInTheDocument();
    expect(screen.queryByText("View contacts")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    expect(screen.getByText("View contacts")).toBeInTheDocument();
  });

  it("resets an override to the default, and only offers it where there is one", async () => {
    const fetchMock = stubAssistantApi();
    renderWithProviders(<AssistantSettingsCard />);
    await screen.findByText("Read invoices");
    expect(screen.getByRole("button", { name: "Reset to default: View customers" })).toBeDisabled();

    const reset = screen.getByRole("button", { name: "Reset to default: Read invoices" });
    expect(reset).toBeEnabled();
    await userEvent.click(reset);
    await waitFor(() =>
      expect(puts(fetchMock).find((call) => call.url.endsWith("invoices%3Aaccess"))?.body).toEqual({ level: null }),
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Level for Read invoices" })).toHaveValue("Read and change"),
    );
    expect(screen.getByRole("button", { name: "Reset to default: Read invoices" })).toBeDisabled();
  });

  it("uses the host's words for modules, categories and permissions", async () => {
    stubAssistantApi({ permissions: permissions() });
    renderWithProviders(
      <AssistantSettingsCard
        translate={{
          module: (group) => `Modul ${group.module}`,
          category: (group) => `Kategori ${group.category}`,
          permission: (permission) => ({ display: `Navn ${permission.key}` }),
        }}
      />,
    );
    expect(await screen.findByText("Navn customers:view")).toBeInTheDocument();
    expect(screen.getByText(/Modul customers\s*›\s*Kategori Customers/)).toBeInTheDocument();
  });
});
