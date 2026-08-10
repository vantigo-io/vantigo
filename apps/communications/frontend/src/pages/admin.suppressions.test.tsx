import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SuppressionsPage } from "./admin.suppressions";

const mocks = vi.hoisted(() => ({
  createSuppression: vi.fn().mockResolvedValue({ id: "s-2" }),
  deleteSuppression: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../api/suppressions", () => ({
  suppressionsQueryOptions: () => ({
    queryKey: ["suppressions"],
    queryFn: vi
      .fn()
      .mockResolvedValue([
        { id: "s-1", emailAddress: "blocked@example.com", reason: "bounce", createdAt: "2026-01-01" },
      ]),
  }),
  createSuppression: mocks.createSuppression,
  deleteSuppression: mocks.deleteSuppression,
}));

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
}));

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SuppressionsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => cleanup());

describe("suppression administration", () => {
  it("renders the suppression list and adds an address", async () => {
    renderPage();
    expect(await screen.findByText("blocked@example.com")).toBeTruthy();
    fireEvent.change(screen.getAllByDisplayValue("")[0], { target: { value: "new@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Add suppression" }));
    await waitFor(() =>
      expect(mocks.createSuppression).toHaveBeenCalledWith(
        { emailAddress: "new@example.com", reason: undefined },
        expect.anything(),
      ),
    );
  });

  it("confirms and deletes an address", async () => {
    renderPage();
    await screen.findByText("blocked@example.com");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByText(/Allow this address to receive messages again/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Delete suppression" }));
    await waitFor(() => expect(mocks.deleteSuppression).toHaveBeenCalledWith("s-1", expect.anything()));
  });
});
