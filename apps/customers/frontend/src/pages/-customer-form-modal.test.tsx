import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerFormModal } from "./-customer-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const renderModal = (state: Parameters<typeof CustomerFormModal>[0]["state"], onClose = vi.fn()) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  );
  render(<CustomerFormModal state={state} onClose={onClose} />, { wrapper });
  return { onClose };
};

describe("CustomerFormModal", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("creates only the safe basic customer payload", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1001 }));
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "  Acme  ");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Acme" }),
    });
  });
  it("does not render aggregate legal identity fields", () => {
    stubFetch(vi.fn());
    renderModal({ mode: "create" });
    expect(screen.queryByLabelText(/legal name/i)).not.toBeInTheDocument();
    expect(screen.getByText(/legal identity is managed separately/i)).toBeInTheDocument();
  });
  it("edits with only the safe basic customer payload", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        jsonResponse(200, { id: 1001, name: "Initrode", timelineSummary: { entryCount: 0, latestOccurredOn: null } }),
      );
    stubFetch(fetchMock);
    const { onClose } = renderModal({
      mode: "edit",
      customer: { id: 1001, name: "Initech", timelineSummary: { entryCount: 0, latestOccurredOn: null } },
    });
    const input = screen.getByLabelText(/name/i);
    await userEvent.clear(input);
    await userEvent.type(input, "Initrode");
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initrode" }),
    });
  });
});
