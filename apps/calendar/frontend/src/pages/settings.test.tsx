import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import "../i18n";
import { problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { feedAddressFixture, stubCalendarServer, subscriptionFixture } from "../test/server";
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
