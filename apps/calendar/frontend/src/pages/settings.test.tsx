import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import "../i18n";
import { problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { feedAddressFixture, mailboxSyncFixture, stubCalendarServer, subscriptionFixture } from "../test/server";
import { SettingsPage } from "./settings";

const card = (name: string) => screen.findByRole("region", { name });

describe("SettingsPage: your calendar on your phone", () => {
  it("offers to create a missing variant's address, and shows a present one with Copy, Reset and Turn off", async () => {
    stubCalendarServer({ feed: { public: true, addresses: [feedAddressFixture("full")] } });
    renderWithProviders(<SettingsPage />);

    expect(await screen.findByRole("heading", { name: "My calendar" })).toBeInTheDocument();
    const full = await card("Full calendar");
    expect(await within(full).findByRole("textbox", { name: "Address" })).toHaveValue(
      "https://vantigo.example/api/v1/calendar/feed/full-token.ics",
    );
    expect(within(full).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(within(full).getByRole("button", { name: "Copy as webcal" })).toBeInTheDocument();
    expect(within(full).getByRole("button", { name: "Reset" })).toBeInTheDocument();
    expect(within(full).getByRole("button", { name: "Turn off" })).toBeInTheDocument();
    expect(within(full).getByText(/never read/)).toBeInTheDocument();

    const busy = await card("Busy only");
    expect(within(busy).getByRole("button", { name: "Create address" })).toBeInTheDocument();
    expect(within(busy).queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByText(/Your phone decides how often it refreshes/)).toBeInTheDocument();
    expect(screen.queryByText("This address works on the company's network only.")).not.toBeInTheDocument();
  });

  it("copies the address in its webcal form", async () => {
    const user = userEvent.setup();
    stubCalendarServer({ feed: { public: true, addresses: [feedAddressFixture("full")] } });
    renderWithProviders(<SettingsPage />);

    const full = await card("Full calendar");
    await user.click(await within(full).findByRole("button", { name: "Copy as webcal" }));

    expect(await navigator.clipboard.readText()).toBe("webcal://vantigo.example/api/v1/calendar/feed/full-token.ics");
    expect(await screen.findByText("Address copied")).toBeInTheDocument();
  });

  it("says when the address was last read", async () => {
    const lastUsedAt = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();
    stubCalendarServer({ feed: { public: true, addresses: [feedAddressFixture("full", { lastUsedAt })] } });
    renderWithProviders(<SettingsPage />);

    const full = await card("Full calendar");
    expect(await within(full).findByText(/last read 2 hours ago/)).toBeInTheDocument();
  });

  it("creates a missing address", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer();
    renderWithProviders(<SettingsPage />);

    const busy = await card("Busy only");
    await user.click(await within(busy).findByRole("button", { name: "Create address" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/calendar/feed/busy"));
    expect(await within(busy).findByRole("textbox", { name: "Address" })).toHaveValue(
      "https://vantigo.example/api/v1/calendar/feed/busy-token-1.ics",
    );
  });

  it("asks before a reset, and posts it only once confirmed", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({ feed: { public: true, addresses: [feedAddressFixture("full")] } });
    renderWithProviders(<SettingsPage />);

    const full = await card("Full calendar");
    await user.click(await within(full).findByRole("button", { name: "Reset" }));
    const confirm = await screen.findByRole("dialog", { name: "Reset the address?" });
    expect(confirm).toHaveTextContent("Your phone will stop updating until you give it the new address.");
    expect(sentAll(fetchMock, "POST")).toHaveLength(0);
    await user.click(within(confirm).getByRole("button", { name: "Reset" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/calendar/feed/full"));
    expect(await within(full).findByDisplayValue(/full-token-1\.ics$/)).toBeInTheDocument();
  });

  it("asks before turning an address off, then deletes it", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({ feed: { public: true, addresses: [feedAddressFixture("full")] } });
    renderWithProviders(<SettingsPage />);

    const full = await card("Full calendar");
    await user.click(await within(full).findByRole("button", { name: "Turn off" }));
    const confirm = await screen.findByRole("dialog", { name: "Turn off the address?" });
    await user.click(within(confirm).getByRole("button", { name: "Turn off" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/calendar/feed/full"));
    expect(await within(full).findByRole("button", { name: "Create address" })).toBeInTheDocument();
  });

  it("says the address works on the company's network only when it is not on a public host", async () => {
    stubCalendarServer({ feed: { public: false, addresses: [feedAddressFixture("full")] } });
    renderWithProviders(<SettingsPage />);

    expect(await screen.findByText("This address works on the company's network only.")).toBeInTheDocument();
  });
});

describe("SettingsPage: calendars from elsewhere", () => {
  it("lists the subscriptions, the host, busy only and the error in red", async () => {
    stubCalendarServer({
      subscriptions: [
        subscriptionFixture(),
        subscriptionFixture({
          id: 1002,
          name: "Partner's calendar",
          url: "https://calendar.partner.example/private/abc.ics",
          colour: "grape",
          busyOnly: true,
          error: "The address answered a page, not a calendar",
        }),
      ],
    });
    renderWithProviders(<SettingsPage />);

    const table = await screen.findByRole("table", { name: "Calendars from elsewhere" });
    const club = within(table).getByText("Football club").closest("tr") as HTMLElement;
    expect(within(club).getByText("club.example")).toBeInTheDocument();
    expect(within(club).queryByText("Busy only")).not.toBeInTheDocument();
    expect(within(club).getByText(/^Updated /)).toBeInTheDocument();
    const partner = within(table).getByText("Partner's calendar").closest("tr") as HTMLElement;
    expect(within(partner).getByText("calendar.partner.example")).toBeInTheDocument();
    expect(within(partner).getByText("Busy only")).toBeInTheDocument();
    const error = within(partner).getByText("The address answered a page, not a calendar");
    expect(error).toHaveStyle({ color: "var(--mantine-color-red-text)" });
  });

  it("adds a calendar, posting the write", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({ subscriptions: [subscriptionFixture()] });
    renderWithProviders(<SettingsPage />);

    await user.click(await screen.findByRole("button", { name: "Add calendar" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a calendar" });
    expect(dialog).toHaveTextContent("The address a calendar program gives you to subscribe to");
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "School holidays");
    await user.type(within(dialog).getByRole("textbox", { name: "Address" }), "webcal://school.example/terms.ics");
    await user.click(within(dialog).getByRole("radio", { name: "Orange" }));
    await user.click(within(dialog).getByRole("switch", { name: /^Show as busy only/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toMatchObject({
        url: "/api/v1/calendar/subscriptions",
        body: { name: "School holidays", url: "webcal://school.example/terms.ics", colour: "orange", busyOnly: true },
      }),
    );
    expect(await screen.findByText("School holidays")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Add a calendar" })).not.toBeInTheDocument();
  });

  it("offers a colour no other subscription has taken", async () => {
    const user = userEvent.setup();
    stubCalendarServer({ subscriptions: [subscriptionFixture({ colour: "blue" })] });
    renderWithProviders(<SettingsPage />);

    await user.click(await screen.findByRole("button", { name: "Add calendar" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a calendar" });
    expect(within(dialog).getByRole("radio", { name: "Cyan" })).toBeChecked();
    expect(within(dialog).getByRole("radio", { name: "Blue" })).not.toBeChecked();
  });

  it("shows the server's sentence on the address when it refuses it", async () => {
    const user = userEvent.setup();
    stubCalendarServer({
      refuse: {
        POST: problemResponse(400, "Validation failed", {
          errors: { url: ["The address may not carry a user name or a password"] },
        }),
      },
    });
    renderWithProviders(<SettingsPage />);

    await user.click(await screen.findByRole("button", { name: "Add calendar" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a calendar" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Secret");
    await user.type(within(dialog).getByRole("textbox", { name: "Address" }), "https://user:pw@host.example/x.ics");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("The address may not carry a user name or a password")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Address" })).toHaveAttribute("aria-invalid", "true");
  });

  it("keeps a saved calendar whose fetch failed, and says why", async () => {
    const user = userEvent.setup();
    // The server saves the row whatever the fetch answers, its error set.
    const error = "The address could not be reached";
    stubCalendarServer({ created: { error, fetchedAt: undefined } });
    renderWithProviders(<SettingsPage />);

    await user.click(await screen.findByRole("button", { name: "Add calendar" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a calendar" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Broken");
    await user.type(within(dialog).getByRole("textbox", { name: "Address" }), "https://broken.example/x.ics");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved, but the calendar could not be fetched")).toBeInTheDocument();
    const row = (await screen.findByText("Broken")).closest("tr") as HTMLElement;
    expect(within(row).getByText(error)).toBeInTheDocument();
    expect(within(row).getByText("Not fetched yet")).toBeInTheDocument();
  });

  it("refreshes a row now and draws it as the fetch left it", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      subscriptions: [subscriptionFixture({ error: "The calendar did not answer in time" })],
    });
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Football club")).closest("tr") as HTMLElement;
    expect(within(row).getByText("The calendar did not answer in time")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Refresh now" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/calendar/subscriptions/1001/refresh"));
    await waitFor(() => expect(screen.queryByText("The calendar did not answer in time")).not.toBeInTheDocument());
    expect(
      within(screen.getByText("Football club").closest("tr") as HTMLElement).getByText(/Updated/),
    ).toHaveTextContent(/now|second|minute/);
  });

  it("edits a row with the same form, and asks before removing one", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({ subscriptions: [subscriptionFixture()] });
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Football club")).closest("tr") as HTMLElement;
    await user.click(within(row).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the calendar" });
    expect(within(dialog).getByRole("textbox", { name: "Address" })).toHaveValue("https://club.example/calendar.ics");
    expect(within(dialog).getByRole("radio", { name: "Teal" })).toBeChecked();
    await user.clear(within(dialog).getByRole("textbox", { name: "Name" }));
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Club");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toMatchObject({
        url: "/api/v1/calendar/subscriptions/1001",
        body: { name: "Club", url: "https://club.example/calendar.ics", colour: "teal", busyOnly: false },
      }),
    );

    const renamed = (await screen.findByText("Club")).closest("tr") as HTMLElement;
    await user.click(within(renamed).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove the calendar?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/calendar/subscriptions/1001"));
    expect(await screen.findByText("No calendars from elsewhere")).toBeInTheDocument();
  });
});

describe("SettingsPage: Outlook", () => {
  afterEach(() => setLanguagePreference("auto"));

  const outlook = () => card("Outlook");
  const toggle = async () => within(await outlook()).findByRole("switch", { name: "Sync to my Outlook calendar" });

  it.each([
    ["switch_off", "Your administrator has not turned on calendar sync."],
    ["no_provider", "Calendar sync needs the Microsoft Entra ID module."],
    ["permission_missing", "Calendar sync is waiting for your administrator to grant the calendar permission."],
    ["unavailable", "The connection to Microsoft 365 is not working right now."],
    ["no_account", "Your Vantigo user is not connected to a Microsoft 365 account that Vantigo manages."],
  ] as const)("says why it cannot be turned on when %s", async (availability, words) => {
    stubCalendarServer({ mailboxSync: mailboxSyncFixture({ availability }) });
    renderWithProviders(<SettingsPage />);

    expect(await toggle()).toBeDisabled();
    expect(within(await outlook()).getByText(words)).toBeInTheDocument();
  });

  it("still lets a person who has it on turn it off while it cannot run", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      mailboxSync: mailboxSyncFixture({ availability: "switch_off", enabled: true, state: "unavailable", revision: 4 }),
    });
    renderWithProviders(<SettingsPage />);

    const on = await toggle();
    expect(on).toBeEnabled();
    expect(on).toBeChecked();
    expect(within(await outlook()).getByRole("radio", { name: /^A separate Vantigo calendar/ })).toBeDisabled();
    expect(within(await outlook()).getByRole("button", { name: "Sync now" })).toBeDisabled();
    await user.click(on);

    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toMatchObject({
        url: "/api/v1/calendar/mailbox-sync",
        headers: { "if-match": "4" },
        body: { enabled: false },
      }),
    );
    expect(await within(await outlook()).findByText("Removing what Vantigo put in Outlook…")).toBeInTheDocument();
  });

  it("turns it on at revision 0 with the defaults, then offers where, what and titles", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer();
    renderWithProviders(<SettingsPage />);

    const off = await toggle();
    expect(off).not.toBeChecked();
    expect(within(await outlook()).queryByRole("radiogroup", { name: "Where" })).not.toBeInTheDocument();
    await user.click(off);

    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toEqual({
        url: "/api/v1/calendar/mailbox-sync",
        headers: expect.objectContaining({ "if-match": "0" }),
        body: { enabled: true, target: "default", detail: "full", kinds: ["calendar.appointment", "staff.absence"] },
      }),
    );
    const region = await outlook();
    expect(await within(region).findByText("Waiting for the first sync…")).toBeInTheDocument();
    expect(within(region).getByRole("radio", { name: /^My main calendar/ })).toBeChecked();
    expect(
      within(region).getByText("Shows the time as busy to colleagues booking meetings in Outlook."),
    ).toBeInTheDocument();
    expect(within(region).getByText("Easy to hide, but colleagues will not see the time as busy.")).toBeInTheDocument();
    expect(within(region).getByRole("checkbox", { name: "Appointments" })).toBeChecked();
    expect(within(region).getByRole("checkbox", { name: "Absence" })).toBeChecked();
    expect(within(region).getByRole("checkbox", { name: "Tasks" })).not.toBeChecked();
    expect(within(region).getByRole("radio", { name: "Full titles" })).toBeChecked();
  });

  it("saves each change at once under the revision it read: a kind, the target and the titles", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      mailboxSync: mailboxSyncFixture({
        enabled: true,
        state: "active",
        revision: 2,
        lastSyncedAt: new Date().toISOString(),
      }),
    });
    renderWithProviders(<SettingsPage />);

    const region = await outlook();
    expect(await within(region).findByText(/^Last synced /)).toBeInTheDocument();
    await user.click(await within(region).findByRole("checkbox", { name: "Tasks" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sentAll(fetchMock, "PUT")[0]).toMatchObject({
      headers: { "if-match": "2" },
      body: { kinds: ["calendar.appointment", "staff.absence", "projects.task"] },
    });
    await waitFor(() => expect(within(region).getByRole("checkbox", { name: "Tasks" })).toBeChecked());

    await user.click(within(region).getByRole("radio", { name: /^A separate Vantigo calendar/ }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(2));
    expect(sentAll(fetchMock, "PUT")[1]).toMatchObject({ headers: { "if-match": "3" }, body: { target: "own" } });

    await waitFor(() => expect(within(region).getByRole("radio", { name: /^Busy only/ })).toBeEnabled());
    await user.click(within(region).getByRole("radio", { name: /^Busy only/ }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(3));
    expect(sentAll(fetchMock, "PUT")[2]).toMatchObject({
      headers: { "if-match": "4" },
      body: { enabled: true, target: "own", detail: "busy" },
    });
  });

  it("reloads when the setting moved on elsewhere", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      mailboxSync: mailboxSyncFixture({ enabled: true, state: "active" }),
      mailboxSyncRefusal: problemResponse(409, "Conflict", { code: "revision_mismatch" }),
    });
    renderWithProviders(<SettingsPage />);

    await user.click(await within(await outlook()).findByRole("checkbox", { name: "Tasks" }));
    expect(
      await screen.findByText("Your Outlook setting was changed elsewhere; it has been reloaded"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(sentAll(fetchMock, "GET").filter((call) => call.url === "/api/v1/calendar/mailbox-sync")).toHaveLength(2),
    );
  });

  it("syncs now, and says why the last sync failed", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      mailboxSync: mailboxSyncFixture({ enabled: true, state: "failed", error: "mailbox_denied" }),
    });
    renderWithProviders(<SettingsPage />);

    const region = await outlook();
    expect(
      await within(region).findByText("Your organisation has not allowed Vantigo to reach your mailbox."),
    ).toBeInTheDocument();
    await user.click(within(region).getByRole("button", { name: "Sync now" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/calendar/mailbox-sync/run"));
    expect(await screen.findByText("Vantigo will sync your calendar within a minute")).toBeInTheDocument();
  });

  it("says the busy time is read, never titles, and when", async () => {
    stubCalendarServer({
      mailboxSync: mailboxSyncFixture({
        enabled: true,
        state: "active",
        lastSyncedAt: new Date().toISOString(),
        busySyncedAt: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
      }),
    });
    renderWithProviders(<SettingsPage />);

    const region = await outlook();
    expect(
      await within(region).findByText(/busy in your main Outlook calendar shows in your Vantigo calendar/),
    ).toBeInTheDocument();
    expect(within(region).getByText(/Their titles are never read/)).toBeInTheDocument();
    expect(within(region).getByText("Busy time read 2 hours ago")).toBeInTheDocument();
  });

  it("explains the two-way sync, and the extra for a separate calendar", async () => {
    stubCalendarServer({ mailboxSync: mailboxSyncFixture({ enabled: true, state: "active" }) });
    renderWithProviders(<SettingsPage />);
    const region = await outlook();
    expect(
      await within(region).findByText(/come back to Vantigo\. Changes to appointments you attend/),
    ).toBeInTheDocument();
    expect(within(region).queryByText(/new events you create in this calendar/)).not.toBeInTheDocument();
  });

  it("says new events become appointments under a separate calendar", async () => {
    stubCalendarServer({ mailboxSync: mailboxSyncFixture({ enabled: true, state: "active", target: "own" }) });
    renderWithProviders(<SettingsPage />);
    expect(await within(await outlook()).findByText(/new events you create in this calendar/)).toBeInTheDocument();
  });

  it.each([
    ["conflict", "You changed “Review” in Outlook while it was changed in Vantigo too; Vantigo's version was kept."],
    ["refused", "Your change to “Review” in Outlook broke one of Vantigo's rules and was put back."],
    [
      "not_owner",
      "“Review” is someone else's appointment; change it in Vantigo or ask its organizer. Your change in Outlook was put back.",
    ],
    [
      "not_editable",
      "Only an appointment's own times can be changed from Outlook for a single occurrence, and Vantigo's other items cannot be changed there; your change was put back.",
    ],
  ] as const)("names what was put back: %s", async (reason, words) => {
    stubCalendarServer({
      mailboxSync: mailboxSyncFixture({
        enabled: true,
        state: "active",
        putBack: {
          reason,
          at: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
          appointmentId: 7,
          title: "Review",
        },
      }),
    });
    renderWithProviders(<SettingsPage />);
    const region = await outlook();
    expect(await within(region).findByText("A change from Outlook was not taken")).toBeInTheDocument();
    expect(within(region).getByText(words)).toBeInTheDocument();
    expect(within(region).getByText("2 hours ago")).toBeInTheDocument();
  });

  it("falls back to a generic name when the title is absent", async () => {
    stubCalendarServer({
      mailboxSync: mailboxSyncFixture({
        enabled: true,
        state: "active",
        putBack: { reason: "conflict", at: new Date().toISOString() },
      }),
    });
    renderWithProviders(<SettingsPage />);
    expect(await within(await outlook()).findByText(/You changed “an appointment” in Outlook/)).toBeInTheDocument();
  });

  it("speaks Norwegian about two-way and what was put back", async () => {
    setLanguagePreference("nb");
    stubCalendarServer({
      mailboxSync: mailboxSyncFixture({
        enabled: true,
        state: "active",
        target: "own",
        putBack: { reason: "not_owner", at: new Date().toISOString(), title: "Gjennomgang" },
      }),
    });
    renderWithProviders(<SettingsPage />);
    const region = await card("Outlook");
    expect(await within(region).findByText(/nye hendelser du oppretter i denne kalenderen/)).toBeInTheDocument();
    expect(within(region).getByText("En endring fra Outlook ble ikke tatt med")).toBeInTheDocument();
    expect(within(region).getByText(/«Gjennomgang» er en annen sin avtale/)).toBeInTheDocument();
  });

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    stubCalendarServer({ mailboxSync: mailboxSyncFixture({ enabled: true, state: "pending" }) });
    renderWithProviders(<SettingsPage />);

    const region = await card("Outlook");
    expect(await within(region).findByRole("switch", { name: "Synkroniser til Outlook-kalenderen min" })).toBeChecked();
    expect(within(region).getByRole("radio", { name: /^Hovedkalenderen min/ })).toBeChecked();
    expect(within(region).getByRole("radio", { name: /^En egen Vantigo-kalender/ })).toBeInTheDocument();
    expect(within(region).getByRole("checkbox", { name: "Fravær" })).toBeChecked();
    expect(within(region).getByRole("radio", { name: "Fulle titler" })).toBeChecked();
    expect(within(region).getByRole("button", { name: "Synkroniser nå" })).toBeInTheDocument();
  });
});
