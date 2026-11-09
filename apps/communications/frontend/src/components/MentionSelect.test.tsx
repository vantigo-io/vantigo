import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { MentionSelect } from "./MentionSelect";

vi.mock("../api/conversations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/conversations")>()),
  assignableUsersQueryOptions: () => ({
    queryKey: ["assignable-users-mention-test"],
    queryFn: () =>
      Promise.resolve([
        { userId: "u-agent", displayName: "Hanna", kind: "agent" },
        { userId: "u-person", displayName: "Kari", kind: "person" },
      ]),
  }),
}));

const mount = (onPick: (mention: { userId: string; displayName: string }) => void) =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MentionSelect onPick={onPick} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("the @ picker of an internal note", () => {
  afterEach(cleanup);

  it("offers the people and the agents, the agents with the AI badge", async () => {
    mount(() => {});
    fireEvent.click(screen.getByPlaceholderText("Mention a colleague with @"));
    expect(await screen.findByRole("option", { name: /Kari/ })).toBeInTheDocument();
    const hanna = await screen.findByRole("option", { name: /Hanna/ });
    expect(hanna.querySelector("[data-testid=ai-badge]")).not.toBeNull();
    expect(screen.getByRole("option", { name: /Kari/ }).querySelector("[data-testid=ai-badge]")).toBeNull();
  });

  it("hands over the id and the name of the colleague picked", async () => {
    const onPick = vi.fn();
    mount(onPick);
    fireEvent.click(screen.getByPlaceholderText("Mention a colleague with @"));
    fireEvent.click(await screen.findByRole("option", { name: /Hanna/ }));
    await waitFor(() => expect(onPick).toHaveBeenCalledWith({ userId: "u-agent", displayName: "Hanna" }));
  });
});
