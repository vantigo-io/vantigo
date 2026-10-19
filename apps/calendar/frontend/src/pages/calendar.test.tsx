import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference, setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import "../i18n";
import { renderWithProviders } from "../test/render";
import { CalendarPage } from "./calendar";

describe("CalendarPage", () => {
  afterEach(() => {
    setTimeZones(null);
    setLanguagePreference("auto");
  });

  it("draws the month with the timed, all-day and multi-day items", async () => {
    renderWithProviders(<CalendarPage />);

    expect(await screen.findByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(screen.getByText("Invoice run")).toBeInTheDocument();
    // The three-day item may cross a week boundary and be drawn as two bars.
    expect(screen.getAllByText("Trade fair, Lillestrøm").length).toBeGreaterThan(0);
    expect(screen.getByRole("radio", { name: "Month" })).toBeChecked();
  });

  it("switches to the week's time grid", async () => {
    const user = userEvent.setup();
    renderWithProviders(<CalendarPage />);
    await screen.findByText("Kickoff with Nordlys AS");
    expect(screen.queryByText("All-day")).not.toBeInTheDocument();

    await user.click(screen.getByText("Week"));

    expect(screen.getByRole("radio", { name: "Week" })).toBeChecked();
    // The time grid's all-day row, and the kickoff — today's, so in this week
    // whichever weekday today is — at its wall-clock time in the viewer's zone
    // (the suite runs in New York, and the instant was made from 09:00 there).
    expect(await screen.findByText("All-day")).toBeInTheDocument();
    expect(screen.getByText("9:00 – 10:30")).toBeInTheDocument();
    expect(screen.getByText("Kickoff with Nordlys AS")).toBeInTheDocument();
  });

  it("draws the grid in the viewer's own zone, not the machine's", async () => {
    // Tokyo is thirteen or fourteen hours from the suite's New York: an
    // instant made from 09:00 Tokyo would read 19:00 or 20:00 the day before
    // if FullCalendar fell back to the machine's zone.
    setTimeZones({ timeZone: "Asia/Tokyo", timeZoneSource: "personal" });
    const user = userEvent.setup();
    renderWithProviders(<CalendarPage />);
    await screen.findByText("Kickoff with Nordlys AS");

    await user.click(screen.getByText("Day"));

    expect(await screen.findByText("9:00 – 10:30")).toBeInTheDocument();
    expect(screen.getByText("Invoice run")).toBeInTheDocument();
  });

  it("speaks Norwegian, controls and grid alike", async () => {
    setLanguagePreference("nb");
    const user = userEvent.setup();
    renderWithProviders(<CalendarPage />);
    await screen.findByText("Kickoff with Nordlys AS");

    expect(screen.getByRole("heading", { name: "Kalender" })).toBeInTheDocument();
    expect(screen.getByText("man.")).toBeInTheDocument();
    await user.click(screen.getByText("Uke"));

    expect(await screen.findByText(/Hele\s+dagen/)).toBeInTheDocument();
    expect(screen.getByText("09:00–10:30")).toBeInTheDocument();
  });
});
