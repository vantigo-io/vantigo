import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, type MockInstance } from "vitest";
import type { StaffStub } from "./server";

/** The writes the fake received, matching a method and a path, with their bodies and If-Match. */
export const sent = (fetchMock: StaffStub, method: string, pattern: RegExp) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && pattern.test(String(url)))
    .map(([url, init]) => ({
      url: String(url),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      ifMatch: new Headers(init?.headers).get("If-Match"),
      cache: init?.cache,
    }));

/** A labelled part of a page — a field group, a card — by its accessible name. */
export const region = (name: string | RegExp) => screen.findByRole("region", { name });

/** Picks an option of a Mantine select inside `container` — the `index`th of that label, for a list's rows. */
export const choose = async (container: HTMLElement, label: string | RegExp, option: string | RegExp, index = 0) => {
  await userEvent.click(within(container).getAllByRole("combobox", { name: label })[index]);
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

/** Replaces what a text field holds. */
export const retype = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  if (text) await userEvent.type(field, text);
};

/**
 * Waits for a download's anchor click. Fetch, `response.blob()`, the object
 * URL and the click take turns of their own, which on a loaded CI runner
 * outlast testing-library's one-second default — and the host's suite runs
 * these tests under its own setup, which does not raise it.
 */
export const awaitDownload = (click: MockInstance) =>
  waitFor(() => expect(click).toHaveBeenCalled(), { timeout: 5000 });
