import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import type { Agent, AgentMemoryCounts } from "../api/types";
import { MemorySummary } from "./memory-summary";

const mocks = vi.hoisted(() => ({ counts: vi.fn() }));

vi.mock("../api/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/agents")>()),
  memoryCountsQueryOptions: (agentId: string) => ({
    queryKey: ["agents", "memory", agentId],
    queryFn: () => mocks.counts(agentId),
  }),
}));

const agent = { id: "a1", name: "Hanna" } as Agent;

const renderSummary = (counts: AgentMemoryCounts | Error) => {
  if (counts instanceof Error) mocks.counts.mockRejectedValue(counts);
  else mocks.counts.mockResolvedValue(counts);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <MemorySummary agent={agent} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("the memory summary", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("counts the notes the agent keeps", async () => {
    renderSummary({ customerNotes: 12, conversationNotes: 3, customers: 5 });
    expect(await screen.findByText("What Hanna remembers")).toBeInTheDocument();
    expect(
      screen.getByText("12 notes about 5 customers and 3 about conversations with no customer."),
    ).toBeInTheDocument();
  });

  it("says when nothing is kept", async () => {
    renderSummary({ customerNotes: 0, conversationNotes: 0, customers: 0 });
    expect(await screen.findByText("Hanna has not kept any notes.")).toBeInTheDocument();
  });

  it("shows nothing when the counts cannot be read", async () => {
    renderSummary(new Error("forbidden"));
    await waitFor(() => expect(mocks.counts).toHaveBeenCalled());
    expect(screen.queryByTestId("memory-summary")).not.toBeInTheDocument();
  });
});
