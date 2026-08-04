import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { MessagesPage } from "./messages.index";

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => ({
    ...options,
    useSearch: () => ({ page: 1 }),
    useNavigate: () => vi.fn(),
  }),
  Link: ({ children }: { children: ReactNode }) => <a href="/messages">{children}</a>,
}));

const page = (data: unknown[]) => ({
  data,
  pagination: {
    page: 1,
    pageSize: 20,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});
const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MessagesPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("message history", () => {
  it("shows the intentional empty state", async () => {
    document.body.innerHTML = "";
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(page([])), { status: 200 })));
    renderPage();
    expect(await screen.findByText("No messages yet")).toBeTruthy();
  });

  it("shows a primary delivery status", async () => {
    document.body.innerHTML = "";
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify(
            page([
              {
                id: "m-1",
                subject: "Welcome",
                recipientCount: 1,
                status: "relay_accepted",
                source: null,
                createdAt: "2026-01-01T00:00:00Z",
              },
            ]),
          ),
          { status: 200 },
        ),
      ),
    );
    renderPage();
    await waitFor(() => expect(screen.getByText("Relay accepted")).toBeTruthy());
  });
});
