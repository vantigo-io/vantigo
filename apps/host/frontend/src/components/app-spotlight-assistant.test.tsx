import { MantineProvider } from "@mantine/core";
import { spotlight } from "@mantine/spotlight";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppSpotlight } from "./app-spotlight";
import "../i18n";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useNavigate: () => vi.fn() };
});

const renderSpotlight = (props: { assistantAvailable?: boolean; onAskAssistant?: (question: string) => void }) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <AppSpotlight permissions={[]} isOwner={false} canManageAuthorization={false} enabledModules={[]} {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  spotlight.open();
};

const type = async (value: string) =>
  fireEvent.change(await waitFor(() => screen.getByPlaceholderText("Search customers, contacts...")), {
    target: { value },
  });

describe("Spotlight's fallback to the assistant", () => {
  afterEach(() => {
    cleanup();
    spotlight.close();
  });

  it("offers to ask the assistant when a query matches nothing, and sends the question", async () => {
    const onAskAssistant = vi.fn();
    renderSpotlight({ assistantAvailable: true, onAskAssistant });
    await type("how do I close the month?");

    fireEvent.click(await screen.findByText("Ask the assistant: “how do I close the month?”"));
    expect(onAskAssistant).toHaveBeenCalledWith("how do I close the month?");
    expect(screen.queryByText("Nothing found...")).not.toBeInTheDocument();
  });

  it("does not offer it for a query that has a good match", async () => {
    renderSpotlight({ assistantAvailable: true, onAskAssistant: vi.fn() });
    await type("Dashboard");
    await screen.findByText("Dashboard", { exact: true });
    expect(screen.queryByText(/Ask the assistant/)).not.toBeInTheDocument();
  });

  it("says nothing was found when the person has no assistant", async () => {
    renderSpotlight({ assistantAvailable: false, onAskAssistant: vi.fn() });
    await type("how do I close the month?");
    expect(await screen.findByText("Nothing found...")).toBeInTheDocument();
    expect(screen.queryByText(/Ask the assistant/)).not.toBeInTheDocument();
  });
});
