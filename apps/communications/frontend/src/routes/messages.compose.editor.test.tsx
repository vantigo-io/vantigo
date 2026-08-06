import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ComposePage } from "./messages.compose";

// Unlike messages.compose.test.tsx, this file intentionally does not mock
// tiptap or @mantine/tiptap: it verifies the real editor actually mounts.
vi.mock("../api/messages", () => ({ createMessage: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
  useNavigate: () => vi.fn(),
}));

afterEach(() => cleanup());

describe("message compose editor", () => {
  it("renders the rich text editor with an editable content area and toolbar", async () => {
    const { container } = render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <ComposePage />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await waitFor(() => {
      const content = container.querySelector(".tiptap[contenteditable='true']");
      expect(content).toBeTruthy();
    });
    expect(container.querySelector(".mantine-RichTextEditor-toolbar")).toBeTruthy();
    expect(container.querySelectorAll(".mantine-RichTextEditor-control").length).toBeGreaterThan(0);
  });
});
