import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { HandOffBanner } from "./HandOffBanner";

const mocks = vi.hoisted(() => ({ handOff: vi.fn(), giveBack: vi.fn(), users: vi.fn() }));

vi.mock("../api/conversations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/conversations")>()),
  assignableUsersQueryOptions: () => ({ queryKey: ["assignable-test"], queryFn: () => mocks.users() }),
}));
vi.mock("../api/handoff", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/handoff")>()),
  handOffQueryOptions: (id: string, enabled: boolean) => ({
    queryKey: ["hand-off-test", id],
    queryFn: () => mocks.handOff(id),
    enabled,
  }),
  giveBack: mocks.giveBack,
}));

const renderBanner = (canManage = true) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <HandOffBanner conversationId="c1" canManage={canManage} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("the hand-off banner", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("offers Give back to the agent on a conversation an agent handed off", async () => {
    mocks.users.mockResolvedValue([{ userId: "a", displayName: "Hanna", kind: "agent" }]);
    mocks.handOff.mockResolvedValue({ handedOff: true, at: "2026-11-08T10:00:00Z", reason: "discarded", roleId: "r" });
    mocks.giveBack.mockResolvedValue(undefined);
    renderBanner();
    const button = await screen.findByRole("button", { name: "Give back to the agent" });
    expect(screen.getByText(/a reviewer discarded its reply/)).toBeInTheDocument();
    fireEvent.click(button);
    await waitFor(() => expect(mocks.giveBack).toHaveBeenCalledWith("c1"));
  });

  it("shows no button to a person who may not manage conversations", async () => {
    mocks.users.mockResolvedValue([{ userId: "a", displayName: "Hanna", kind: "agent" }]);
    mocks.handOff.mockResolvedValue({ handedOff: true, at: null, reason: "hand_off", roleId: null });
    renderBanner(false);
    expect(await screen.findByTestId("handed-off")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Give back to the agent" })).toBeNull();
  });

  it("says nothing on a conversation that is not handed off, and asks nothing where there are no agents", async () => {
    mocks.users.mockResolvedValue([{ userId: "p", displayName: "Kari", kind: "person" }]);
    renderBanner();
    await waitFor(() => expect(mocks.users).toHaveBeenCalled());
    expect(mocks.handOff).not.toHaveBeenCalled();
    expect(screen.queryByTestId("handed-off")).toBeNull();

    cleanup();
    mocks.users.mockResolvedValue([{ userId: "a", displayName: "Hanna", kind: "agent" }]);
    mocks.handOff.mockResolvedValue({ handedOff: false, at: null, reason: null, roleId: null });
    renderBanner();
    await waitFor(() => expect(mocks.handOff).toHaveBeenCalled());
    expect(screen.queryByTestId("handed-off")).toBeNull();
  });
});
