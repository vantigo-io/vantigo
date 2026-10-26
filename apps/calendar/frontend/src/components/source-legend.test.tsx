import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { calendarCatalog } from "../i18n";
import { colourForKind, kindLabelKey } from "../lib/colours";
import { HIDDEN_KINDS_STORAGE_KEY, readHiddenKinds, writeHiddenKinds } from "../lib/filter";
import { renderWithProviders } from "../test/render";
import { SourceLegend } from "./source-legend";

describe("the filter's memory", () => {
  beforeEach(() => window.localStorage.removeItem(HIDDEN_KINDS_STORAGE_KEY));
  afterEach(() => window.localStorage.removeItem(HIDDEN_KINDS_STORAGE_KEY));

  it("reads back what it wrote, and nothing from a value it cannot read", () => {
    writeHiddenKinds(["projects.task"]);
    expect(readHiddenKinds()).toEqual(["projects.task"]);
    window.localStorage.setItem(HIDDEN_KINDS_STORAGE_KEY, "{not json");
    expect(readHiddenKinds()).toEqual([]);
  });

  it("names a kind no catalog knows by the kind itself", async () => {
    const toggled: string[] = [];
    renderWithProviders(
      <SourceLegend
        kinds={["calendar.appointment", "energy.reading"]}
        hidden={new Set()}
        onToggle={(kind) => toggled.push(kind)}
      />,
    );
    expect(screen.getByText("Appointments")).toBeInTheDocument();
    await userEvent.click(screen.getByText("energy.reading"));
    expect(toggled).toEqual(["energy.reading"]);
  });

  it("gives the subscriptions one grey chip with a swatch of its own, which hides them all", async () => {
    const toggled: string[] = [];
    renderWithProviders(
      <SourceLegend
        kinds={["calendar.appointment", "calendar.subscription", "holidays"]}
        hidden={new Set()}
        onToggle={(kind) => toggled.push(kind)}
      />,
    );
    const chip = screen.getByRole("checkbox", { name: "Subscriptions" });
    expect(chip).toBeChecked();
    const swatch = document.querySelector('[data-swatch="calendar.subscription"]') as HTMLElement;
    expect(swatch.style.border).toContain("var(--vantigo-calendar-gray)");
    expect(swatch.style.background).toBe("");
    // Not the appointments' colour, which the kind's module would otherwise give it.
    const appointments = document.querySelector('[data-swatch="calendar.appointment"]') as HTMLElement;
    expect(appointments.style.background).toBe("var(--vantigo-calendar-primary)");
    await userEvent.click(screen.getByText("Subscriptions"));
    expect(toggled).toEqual(["calendar.subscription"]);
  });

  it("names Planning's bookings in both languages and draws them in Planning's colour", () => {
    const key = kindLabelKey("planning.allocation") as keyof typeof calendarCatalog.en;
    expect(calendarCatalog.en[key]).toBe("Bookings");
    expect(calendarCatalog.nb[key]).toBe("Allokeringer");
    expect(colourForKind("planning.allocation")).toBe("var(--vantigo-calendar-indigo)");
    renderWithProviders(<SourceLegend kinds={["planning.allocation"]} hidden={new Set()} onToggle={() => {}} />);
    expect(screen.getByRole("checkbox", { name: "Bookings" })).toBeChecked();
  });
});
