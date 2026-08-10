import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ReplaceMeterModal } from "./-replace-meter-modal";

describe("ReplaceMeterModal", () => {
  it("requires a meter number before replacing", async () => {
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <ReplaceMeterModal meteringPointId={1} opened onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Replace meter" }));
    expect(await screen.findByText("Meter number is required")).toBeInTheDocument();
  });
});
