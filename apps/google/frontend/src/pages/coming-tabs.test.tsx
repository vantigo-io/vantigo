import { screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { GoogleCalendarGmailTab } from "./coming-tabs";

afterEach(() => setLanguagePreference("auto"));

describe("the tab still to come", () => {
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
