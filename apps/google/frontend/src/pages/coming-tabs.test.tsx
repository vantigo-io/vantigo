import { screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { GoogleAccountsTab, GoogleCalendarGmailTab } from "./coming-tabs";

afterEach(() => setLanguagePreference("auto"));

describe("the tabs still to come", () => {
  it("says what Accounts will hold, in English and Norwegian", () => {
    const { unmount } = renderWithProviders(<GoogleAccountsTab />);
    expect(
      screen.getByText(
        "Staff will create and keep your people's Google accounts and groups here, and connect existing users to their Google sign-in.",
      ),
    ).toBeInTheDocument();
    unmount();

    setLanguagePreference("nb");
    renderWithProviders(<GoogleAccountsTab />);
    expect(screen.getByText("Kontoer kommer")).toBeInTheDocument();
  });

  it("says what Calendar and Gmail will hold, in English and Norwegian", () => {
    const { unmount } = renderWithProviders(<GoogleCalendarGmailTab />);
    expect(
      screen.getByText(
        "Each person's own Google connection, calendar sync to Google Calendar, Gmail mail channels and sending from a person's Gmail address will be set up here.",
      ),
    ).toBeInTheDocument();
    unmount();

    setLanguagePreference("nb");
    renderWithProviders(<GoogleCalendarGmailTab />);
    expect(screen.getByText("Kalender og Gmail kommer")).toBeInTheDocument();
  });
});
