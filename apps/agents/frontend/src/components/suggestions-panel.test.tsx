import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import type { AgentRole, InstructionSuggestion } from "../api/types";
import { SuggestionsPanel } from "./suggestions-panel";

const mocks = vi.hoisted(() => ({ list: vi.fn(), apply: vi.fn(), dismiss: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({
  Link: (props: Record<string, unknown>) => {
    const { children, ...rest } = props;
    for (const key of ["to", "search", "params"]) delete rest[key];
    return (
      <a href="/agents" {...(rest as object)}>
        {children as never}
      </a>
    );
  },
}));
vi.mock("../api/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/agents")>()),
  suggestionsQueryOptions: (roleId: string) => ({
    queryKey: ["agents", "suggestions", roleId],
    queryFn: () => mocks.list(roleId),
  }),
  applySuggestion: mocks.apply,
  dismissSuggestion: mocks.dismiss,
}));

const role = { id: "ro1", agentId: "a1", name: "Front desk", version: 7 } as AgentRole;

const suggestion = (patch: Partial<InstructionSuggestion> = {}): InstructionSuggestion => ({
  id: "s1",
  roleId: "ro1",
  oldText: null,
  newText: "Offer a callback when the customer asks for a date.",
  rationale: "People removed the promised dates.",
  itemIds: ["i1", "i2", "i3"],
  state: "proposed",
  createdAt: "2026-11-09T06:00:00Z",
  ...patch,
});

const renderPanel = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <Notifications />
        <SuggestionsPanel agentId="a1" role={role} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("the suggestions panel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("shows nothing when nothing is proposed", async () => {
    mocks.list.mockResolvedValue([]);
    renderPanel();
    await waitFor(() => expect(mocks.list).toHaveBeenCalled());
    expect(screen.queryByTestId("suggestions")).not.toBeInTheDocument();
  });

  it("shows what is proposed with the corrections it rests on, and the passage it replaces", async () => {
    mocks.list.mockResolvedValue([
      suggestion(),
      suggestion({ id: "s2", oldText: "Be brief.", newText: "Answer in two sentences.", itemIds: ["i4", "i5"] }),
    ]);
    renderPanel();
    expect(await screen.findAllByTestId("suggestion")).toHaveLength(2);
    expect(screen.getAllByText("People removed the promised dates.")).toHaveLength(2);
    expect(screen.getByText("Rests on 3 corrections:")).toBeInTheDocument();
    expect(screen.getByText("Rests on 2 corrections:")).toBeInTheDocument();
    expect(screen.getByText("Add a paragraph:")).toBeInTheDocument();
    expect(screen.getByText("Replace this passage:")).toBeInTheDocument();
    expect(screen.getByText("Be brief.")).toBeInTheDocument();
  });

  it("applies a suggestion at the role's version with one click", async () => {
    mocks.list.mockResolvedValue([suggestion()]);
    mocks.apply.mockResolvedValue(role);
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Apply to the instructions" }));
    await waitFor(() => expect(mocks.apply).toHaveBeenCalledWith("s1", 7));
    expect(await screen.findByText("The instructions were changed")).toBeInTheDocument();
  });

  it("says why when the passage is gone, and when the role changed meanwhile", async () => {
    mocks.list.mockResolvedValue([suggestion()]);
    mocks.apply.mockRejectedValueOnce(Object.assign(new Error("x"), { code: "suggestion_stale" }));
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Apply to the instructions" }));
    expect(await screen.findByText(/no longer in the instructions/)).toBeInTheDocument();

    mocks.apply.mockRejectedValueOnce(Object.assign(new Error("x"), { code: "version_conflict" }));
    fireEvent.click(await screen.findByRole("button", { name: "Apply to the instructions" }));
    await waitFor(() => expect(mocks.apply).toHaveBeenCalledTimes(2));
  });

  it("dismisses a suggestion without applying it", async () => {
    mocks.list.mockResolvedValue([suggestion()]);
    mocks.dismiss.mockResolvedValue(suggestion({ state: "dismissed" }));
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(mocks.dismiss).toHaveBeenCalledWith("s1"));
    expect(mocks.apply).not.toHaveBeenCalled();
  });
});
