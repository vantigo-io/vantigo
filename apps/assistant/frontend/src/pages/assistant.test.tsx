import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { assistantMessage, conversation, detailOf, stubAssistantApi, userMessage } from "../test/server";
import { AssistantPage } from "./assistant";
import "../i18n";

const today = new Date().toISOString();

const list = [
  conversation({ id: "c1", title: "Pinned question", pinned: true, lastActivityAt: today, originApp: "customers" }),
  conversation({ id: "c2", title: "Metering point steps", lastActivityAt: today, originApp: "energy" }),
  conversation({ id: "c3", title: "Old chat", lastActivityAt: "2025-01-01T10:00:00Z", originApp: "invoices" }),
];

const Page = ({ onSelect = () => {} }: { onSelect?: (id: string | null) => void }) => {
  const [id, setId] = useState<string | null>(null);
  return (
    <AssistantPage
      conversationId={id}
      onSelect={(next) => {
        setId(next);
        onSelect(next);
      }}
      appLabel={(app) => ({ customers: "Customers", energy: "Energy", invoices: "Invoices" })[app] ?? app}
    />
  );
};

describe("the Assistant app", () => {
  it("lists every conversation in groups, with where each began", async () => {
    stubAssistantApi({ conversations: list });
    renderWithProviders(<Page />);
    expect(await screen.findByText("Pinned question")).toBeInTheDocument();
    for (const heading of ["Pinned", "Today", "Older"]) expect(screen.getByText(heading)).toBeInTheDocument();
    expect(screen.queryByText("Yesterday")).not.toBeInTheDocument();
    const badges = screen.getAllByTestId("origin-badge").map((one) => one.textContent);
    expect(badges).toEqual(["Started in Customers", "Started in Energy", "Started in Invoices"]);
  });

  it("searches, and resumes a conversation it opens", async () => {
    const fetchMock = stubAssistantApi({
      conversations: list,
      details: { c2: detailOf(list[1], [userMessage("How?"), assistantMessage("Like this.")]) },
    });
    const onSelect = vi.fn();
    renderWithProviders(<Page onSelect={onSelect} />);
    await screen.findByText("Old chat");
    await userEvent.type(screen.getByRole("textbox", { name: "Search conversations" }), "metering");
    await waitFor(() => expect(screen.queryByText("Old chat")).not.toBeInTheDocument());
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("q=metering"))).toBe(true);

    await userEvent.click(await screen.findByText("Metering point steps"));
    expect(onSelect).toHaveBeenCalledWith("c2");
    expect(await screen.findByText("Like this.")).toBeInTheDocument();
    await waitFor(() =>
      expect(sentAll(fetchMock, "PUT").some((call) => call.url === "/api/v1/assistant/me")).toBe(true),
    );
  });

  it("renames, pins, archives and deletes from the row's menu", async () => {
    const fetchMock = stubAssistantApi({ conversations: list });
    renderWithProviders(<Page />);
    await screen.findByText("Old chat");

    const menu = async (title: string, item: string) => {
      await userEvent.click(screen.getByRole("button", { name: `Actions for ${title}` }));
      await userEvent.click(await screen.findByRole("menuitem", { name: item }));
    };

    await menu("Old chat", "Pin");
    await waitFor(() =>
      expect(sentAll(fetchMock, "PATCH")[0]).toMatchObject({
        url: "/api/v1/assistant/conversations/c3",
        body: { pinned: true },
      }),
    );

    await menu("Old chat", "Archive");
    await waitFor(() => expect(sentAll(fetchMock, "PATCH")[1].body).toEqual({ archived: true }));

    await menu("Old chat", "Rename");
    const box = await screen.findByRole("textbox", { name: "Title" });
    await userEvent.clear(box);
    await userEvent.type(box, "Renamed");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sentAll(fetchMock, "PATCH")[2].body).toEqual({ title: "Renamed" }));

    await menu("Old chat", "Delete");
    const dialog = await screen.findByRole("dialog", { name: "Delete this conversation?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(sentAll(fetchMock, "DELETE")[0]?.url).toBe("/api/v1/assistant/conversations/c3"));
  });

  it("lists the archived ones behind a switch", async () => {
    const fetchMock = stubAssistantApi({ conversations: list });
    renderWithProviders(<Page />);
    await screen.findByText("Old chat");
    await userEvent.click(screen.getByRole("switch", { name: "Archived" }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url).includes("archived=true"))).toBe(true),
    );
  });
});
