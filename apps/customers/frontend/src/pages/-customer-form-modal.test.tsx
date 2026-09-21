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
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  );
  render(<CustomerFormModal state={state} onClose={onClose} />, { wrapper });
  return { onClose };
};

describe("CustomerFormModal", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("creates a business by default, with only the safe basic payload", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1001 }));
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    expect(screen.getByRole("radio", { name: /business/i })).toBeChecked();
    await userEvent.type(screen.getByLabelText(/name/i), "  Acme  ");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Acme", status: "active", type: "business" }),
    });
  });
  it("creates a private customer without ever looking the name up", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) =>
      String(url) === "/api/v1/customers"
        ? Promise.resolve(jsonResponse(201, { id: 1002 }))
        : Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "KARI NORDMANN" }] })),
    );
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));
    expect(screen.queryByText(/brønnøysundregistrene/i)).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/name/i), "Kari Nordmann");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([url]) => String(url)).some((url) => url.includes("/lookup/brreg"))).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Kari Nordmann", status: "active", type: "person" }),
    });
  });
  it("drops a picked business identity when switching to private", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) =>
      String(url) === "/api/v1/customers"
        ? Promise.resolve(jsonResponse(201, { id: 1003 }))
        : Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] })),
    );
    stubFetch(fetchMock);
    const { onClose } = renderModal({ mode: "create" });
    await userEvent.type(screen.getByLabelText(/name/i), "Equinor");
    await userEvent.click(await screen.findByRole("option", { name: /equinor asa/i }));
    expect(screen.getByLabelText(/name/i)).toHaveValue("EQUINOR ASA");
    await userEvent.click(screen.getByRole("radio", { name: /private/i }));
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "EQUINOR ASA", status: "active", type: "person" }),
    });
  });
  it("does not render aggregate legal identity fields", () => {
    stubFetch(vi.fn());
    renderModal({ mode: "create" });
    expect(screen.queryByLabelText(/legal name/i)).not.toBeInTheDocument();
    expect(screen.getByText(/legal identity is managed separately/i)).toBeInTheDocument();
  });
  it("edits without a type toggle and never sends the type", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        jsonResponse(200, { id: 1001, name: "Initrode", timelineSummary: { entryCount: 0, latestOccurredOn: null } }),
      );
    stubFetch(fetchMock);
    const { onClose } = renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
      },
    });
    expect(screen.queryByRole("radio", { name: /business/i })).not.toBeInTheDocument();
    const input = screen.getByLabelText(/name/i);
    await userEvent.clear(input);
    await userEvent.type(input, "Initrode");
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initrode", status: "active" }),
    });
  });
});
