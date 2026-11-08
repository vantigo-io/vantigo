import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CustomerAgentMemory as Memory } from "../api/agent-memory";
import { stubFetch } from "../test/fetch";
import { CustomerAgentMemory } from "./-customer-agent-memory";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const memory = (patch: Partial<Memory> = {}): Memory => ({
  canDelete: true,
  agents: [
    {
      agentId: "a1",
      agentName: "Hanna",
      full: false,
      notes: [
        { id: "n1", text: "Prefers e-mail, never phone.", createdAt: "2026-11-08T10:00:00Z" },
        { id: "n2", text: "Has a dog called Pepper.", createdAt: "2026-11-01T10:00:00Z" },
      ],
    },
  ],
  ...patch,
});

const renderSection = (fetchMock: ReturnType<typeof vi.fn>) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerAgentMemory customerId={42} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

describe("What an agent remembers on the customer page", () => {
  it("is collapsed to its title and the count, and opens to the notes", async () => {
    renderSection(vi.fn().mockResolvedValue(json(200, memory())));
    const toggle = await screen.findByRole("button", { name: /What Hanna remembers/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByText("2 notes")).toBeInTheDocument();
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Prefers e-mail, never phone.")).toBeInTheDocument();
    expect(screen.getByText("Has a dog called Pepper.")).toBeInTheDocument();
  });

  it("shows nothing when nothing is remembered, or the reader may not see it", async () => {
    const fetchMock = vi.fn().mockResolvedValue(json(200, memory({ agents: [], canDelete: false })));
    renderSection(fetchMock);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(screen.queryByTestId("agent-memory")).not.toBeInTheDocument();
  });

  it("deletes a note after a confirmation, and reads the list again", async () => {
    let deleted = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        expect(String(input)).toBe("/api/v1/agents/memory/n1");
        deleted = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      const all = memory();
      return Promise.resolve(
        json(200, deleted ? { ...all, agents: [{ ...all.agents[0], notes: [all.agents[0].notes[1]] }] } : all),
      );
    });
    renderSection(fetchMock);
    await userEvent.click(await screen.findByRole("button", { name: /What Hanna remembers/ }));
    await userEvent.click(
      screen.getByRole("button", { name: "Delete the note: Prefers e-mail, never phone.", hidden: true }),
    );
    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(screen.queryByText("Prefers e-mail, never phone.")).not.toBeInTheDocument());
    expect(screen.getByText("Has a dog called Pepper.")).toBeInTheDocument();
  });

  it("offers no delete to a reader who may not manage the agents", async () => {
    renderSection(vi.fn().mockResolvedValue(json(200, memory({ canDelete: false }))));
    await userEvent.click(await screen.findByRole("button", { name: /What Hanna remembers/ }));
    expect(screen.getByText("Prefers e-mail, never phone.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Delete the note/, hidden: true })).not.toBeInTheDocument();
  });

  it("says when the agent's memory about the customer is full", async () => {
    const all = memory();
    renderSection(vi.fn().mockResolvedValue(json(200, { ...all, agents: [{ ...all.agents[0], full: true }] })));
    await userEvent.click(await screen.findByRole("button", { name: /What Hanna remembers/ }));
    expect(screen.getByText("full")).toBeInTheDocument();
    expect(screen.getByTestId("agent-memory-full")).toHaveTextContent("memory about this customer is full");
  });
});
