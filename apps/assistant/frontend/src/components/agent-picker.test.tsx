import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { assistantMessage, conversation, detailOf, sseResponse, stubAssistantApi, userMessage } from "../test/server";
import { ConversationView } from "./conversation-view";
import "../i18n";

const origin = { app: "agents", route: "/agents" };
const hanna = { id: "a1000000-0000-4000-8000-000000000001", name: "Hanna" };

const doneStream = () =>
  sseResponse([
    ["run", { runId: "run-1" }],
    ["done", { state: "done" }],
  ]);

describe("talking to a virtual employee in the panel", () => {
  it("offers the picker only where the server lists an agent: without agents:view it lists none", async () => {
    stubAssistantApi({ agents: [] });
    renderWithProviders(<ConversationView conversationId={null} origin={origin} onCreated={() => {}} />);
    await screen.findByRole("textbox", { name: "Message to the assistant" });
    expect(screen.queryByTestId("agent-picker")).not.toBeInTheDocument();
  });

  it("starts the conversation with the agent chosen, and with the Assistant when none is", async () => {
    const fetchMock = stubAssistantApi({ agents: [hanna], stream: doneStream });
    renderWithProviders(<ConversationView conversationId={null} origin={origin} onCreated={() => {}} />);
    const picker = await screen.findByRole("combobox", { name: "Who do you want to talk to?" });
    expect(screen.getByRole("option", { name: "The Assistant" })).toBeInTheDocument();
    await userEvent.selectOptions(picker, "Hanna");
    await userEvent.type(screen.getByRole("textbox", { name: "Message to the assistant" }), "Help me{Enter}");
    await waitFor(() => expect(sentAll(fetchMock, "POST").find((r) => r.url.endsWith("/conversations"))).toBeTruthy());
    expect(sentAll(fetchMock, "POST").find((r) => r.url.endsWith("/conversations"))?.body).toEqual({
      origin,
      agentId: hanna.id,
    });
  });

  it("sends no agent when the Assistant itself is chosen", async () => {
    const fetchMock = stubAssistantApi({ agents: [hanna], stream: doneStream });
    renderWithProviders(<ConversationView conversationId={null} origin={origin} onCreated={() => {}} />);
    await screen.findByRole("combobox", { name: "Who do you want to talk to?" });
    await userEvent.type(screen.getByRole("textbox", { name: "Message to the assistant" }), "Help me{Enter}");
    await waitFor(() => expect(sentAll(fetchMock, "POST").find((r) => r.url.endsWith("/conversations"))).toBeTruthy());
    expect(sentAll(fetchMock, "POST").find((r) => r.url.endsWith("/conversations"))?.body).toEqual({ origin });
  });

  it("names the agent in the conversation, with the AI badge, and says what that means", async () => {
    const summary = conversation({ id: "c1", title: "Help", agent: hanna });
    stubAssistantApi({
      details: { c1: detailOf(summary, [userMessage("Hello"), assistantMessage("Hi, I am here.")]) },
    });
    renderWithProviders(<ConversationView conversationId="c1" origin={origin} onCreated={() => {}} />);
    const header = await screen.findByTestId("agent-header");
    expect(header).toHaveTextContent("Hanna");
    expect(header).toHaveTextContent("Hanna is a virtual employee, an AI.");
    expect(header.querySelector("[data-testid=ai-badge]")).not.toBeNull();
    // The picker is for a draft: an agent's conversation does not change who it is with.
    expect(screen.queryByTestId("agent-picker")).not.toBeInTheDocument();
  });

  it("shows no agent header in the Assistant's own conversations", async () => {
    const summary = conversation({ id: "c1", title: "Help" });
    stubAssistantApi({ details: { c1: detailOf(summary, [userMessage("Hello"), assistantMessage("Hi.")]) } });
    renderWithProviders(<ConversationView conversationId="c1" origin={origin} onCreated={() => {}} />);
    await screen.findByText("Hi.");
    expect(screen.queryByTestId("agent-header")).not.toBeInTheDocument();
  });
});
