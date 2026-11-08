import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ConversationListItem } from "../api/conversations";
import "../i18n";
import { ConversationList } from "./ConversationList";

vi.mock("../api/conversations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/conversations")>()),
  assignableUsersQueryOptions: () => ({
    queryKey: ["assignable-users-test"],
    queryFn: () =>
      Promise.resolve([
        { userId: "u-agent", displayName: "Hanna", kind: "agent" },
        { userId: "u-person", displayName: "Kari", kind: "person" },
      ]),
  }),
}));

const row = (id: string, userId: string, name: string) =>
  ({
    id,
    channelId: "c1",
    subject: `Subject ${id}`,
    status: "open",
    assignedUserId: userId,
    assignedUserName: name,
    customerId: null,
    customerAssociationSource: null,
    suggestedCustomerId: null,
    candidateCustomerIds: [],
    tags: [],
    participants: [],
    lastActivityAt: "2026-11-08T10:00:00Z",
    unread: false,
  }) as unknown as ConversationListItem;

describe("the conversation list's assignee avatar", () => {
  afterEach(cleanup);

  it("carries the AI badge for an agent and not for a person", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <MantineProvider env="test">
          <ConversationList
            data={
              {
                data: [row("1", "u-agent", "Hanna"), row("2", "u-person", "Kari")],
                pagination: { totalCount: 2 },
              } as never
            }
            isPending={false}
            isError={false}
            error={null}
            selectedId=""
            onSelect={() => undefined}
            onRefresh={() => undefined}
          />
        </MantineProvider>
      </QueryClientProvider>,
    );
    expect(await screen.findAllByTestId("ai-badge")).toHaveLength(1);
  });
});
