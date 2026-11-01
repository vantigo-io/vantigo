import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { settings, stubAssistantApi } from "../test/server";
import { AssistantPersonSwitch } from "./person-switch";
import "../i18n";

describe("AssistantPersonSwitch", () => {
  it("shows the person's own switch, off until set, and saves it under the version it read", async () => {
    const fetchMock = stubAssistantApi({
      person: { userId: "user-1", enabled: false, version: 0 },
      settings: settings({ mode: "per_person" }),
    });
    renderWithProviders(<AssistantPersonSwitch userId="user-1" />);
    const toggle = await screen.findByRole("switch", { name: /Has the assistant/ });
    expect(toggle).not.toBeChecked();
    expect(await screen.findByText("Turns the assistant on for this person.")).toBeInTheDocument();

    await userEvent.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    const put = sentAll(fetchMock, "PUT")[0];
    expect(put.url).toBe("/api/v1/assistant/people/user-1");
    expect(put.body).toEqual({ enabled: true });
    expect(put.headers["if-match"]).toBe("0");

    await userEvent.click(toggle);
    await waitFor(() => expect(toggle).not.toBeChecked());
    expect(sentAll(fetchMock, "PUT")[1].headers["if-match"]).toBe("1");
  });

  it("says the switch only counts in Per person mode when another mode is set", async () => {
    stubAssistantApi({ settings: settings({ mode: "all" }) });
    renderWithProviders(<AssistantPersonSwitch userId="user-1" />);
    expect(await screen.findByText(/Applies only when the assistant is set to Per person/)).toBeInTheDocument();
  });

  it("reloads and says so when somebody else changed the switch", async () => {
    stubAssistantApi({
      person: { userId: "user-1", enabled: true, version: 2 },
      handle: (method) =>
        method === "PUT"
          ? new Response(JSON.stringify({ title: "stale", status: 409, code: "version_conflict" }), {
              status: 409,
              headers: { "Content-Type": "application/problem+json" },
            })
          : undefined,
    });
    renderWithProviders(<AssistantPersonSwitch userId="user-1" />);
    await userEvent.click(await screen.findByRole("switch", { name: /Has the assistant/ }));
    expect(await screen.findByText("Somebody else saved these settings")).toBeInTheDocument();
  });
});
