import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { formatDate } from "@vantigo/frontend-shell";
import { toUtc } from "../lib/weeks";
import type { PlanningStub } from "./server";

/** The requests the fake received, matching a method and a path, with their bodies and If-Match. */
export const sent = (fetchMock: PlanningStub, method: string, pattern: RegExp) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && pattern.test(String(url)))
    .map(([url, init]) => ({
      url: String(url),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      ifMatch: new Headers(init?.headers).get("If-Match"),
    }));

/** Picks an option of a Mantine select inside `container`. */
export const choose = async (container: HTMLElement, label: string | RegExp, option: string | RegExp) => {
  await userEvent.click(within(container).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

/** Replaces what a text field holds. */
export const retype = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  if (text) await userEvent.type(field, text);
};

/** A day as the package shows it (`usePlanningFormat().date`). */
export const shownDate = (iso: string) =>
  formatDate(toUtc(iso), { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });

/** The day of an instant as the package shows it (`usePlanningFormat().stamp`). */
export const shownStamp = (instant: string) =>
  formatDate(new Date(instant), { day: "numeric", month: "short", year: "numeric" });
