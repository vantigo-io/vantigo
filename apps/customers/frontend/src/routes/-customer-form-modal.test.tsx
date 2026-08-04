import { MantineProvider } from "@mantine/core";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderModal = (state: CustomerModalState, onClose = vi.fn()) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

  const wrapper = ({ children }: { children: ReactNode }) => (
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  );

  render(<CustomerFormModal state={state} onClose={onClose} />, { wrapper });

  return { onClose, invalidateSpy };
};

describe("CustomerFormModal", () => {
  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("creates a customer and shows a success notification", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1001 }));
    stubFetch(fetchMock);

    const { onClose, invalidateSpy } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/name/i), "  Acme  ");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Acme", identity: null }),
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["customers"] });
    expect(await screen.findByText("Customer created")).toBeInTheDocument();
  });

  it("shows a client-side validation error without calling the API", async () => {
    const fetchMock = vi.fn();
    stubFetch(fetchMock);

    renderModal({ mode: "create" });

    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(await screen.findByText("Name is required")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("maps server validation errors onto the name field", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          title: "Invalid customer",
          status: 400,
          errors: { name: ["A friendly name cannot exceed 100 characters"] },
        }),
      ),
    );

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/name/i), "Acme");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(await screen.findByText("A friendly name cannot exceed 100 characters")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("shows an error notification when the request fails unexpectedly", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 500 })));

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/name/i), "Acme");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(await screen.findByText("Failed to create customer")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("prefills the current name and PUTs to the customer url when editing", async () => {
    const customer = { id: 1001, name: "Initech", identity: null };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...customer, name: "Initrode" }));
    stubFetch(fetchMock);

    const { onClose } = renderModal({ mode: "edit", customer });

    const nameInput = screen.getByLabelText(/name/i);
    expect(nameInput).toHaveValue("Initech");

    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "Initrode");
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initrode", identity: null }),
    });
    expect(await screen.findByText("Customer updated")).toBeInTheDocument();
  });

  it("shows the legal identity fields only after opting in", async () => {
    stubFetch(vi.fn());

    renderModal({ mode: "create" });

    expect(screen.queryByLabelText(/legal name/i)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("switch"));

    expect(screen.getByText("Country")).toBeInTheDocument();
    expect(screen.getByText("Type")).toBeInTheDocument();
    expect(screen.getByLabelText(/legal name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/^legal id \*/i)).toBeInTheDocument();
  });

  it("suggests registry matches while typing and fills legal id and name on selection", async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.startsWith("/api/v1/lookup/brreg")) {
        return Promise.resolve(
          jsonResponse(200, {
            data: [
              { legalId: "923609016", legalName: "EQUINOR ASA" },
              { legalId: "914778271", legalName: "EQUINOR ENERGY AS" },
            ],
          }),
        );
      }
      return Promise.resolve(jsonResponse(201, { id: 1001 }));
    });
    stubFetch(fetchMock);

    renderModal({ mode: "create" });

    await userEvent.click(screen.getByRole("switch"));
    await userEvent.type(screen.getByLabelText(/legal name/i), "equinor");

    await userEvent.click(await screen.findByText("EQUINOR ASA"));

    expect(screen.getByLabelText(/legal name/i)).toHaveValue("EQUINOR ASA");
    expect(screen.getByLabelText(/^legal id \*/i)).toHaveValue("923609016");

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/lookup/brreg?search=equinor", expect.anything());
  });

  it("submits the legal identity when opted in", async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.startsWith("/api/v1/lookup/brreg")) {
        return Promise.resolve(jsonResponse(200, { data: [] }));
      }
      return Promise.resolve(jsonResponse(201, { id: 1001 }));
    });
    stubFetch(fetchMock);

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/^name/i), "Acme");
    await userEvent.click(screen.getByRole("switch"));
    await userEvent.type(screen.getByLabelText(/legal name/i), "Acme AS");
    await userEvent.type(screen.getByLabelText(/^legal id \*/i), "923609016");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Acme",
        identity: { country: "no", type: "business", id: "923609016", name: "Acme AS", source: "manual" },
      }),
    });
  });

  it("prefills the legal identity when editing a customer that has one", async () => {
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, { data: [] })));

    renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        name: "Equinor",
        identity: { country: "no", type: "business", id: "923609016", name: "EQUINOR ASA", source: "brreg" },
      },
    });

    expect(screen.getByRole("switch")).toBeChecked();
    expect(screen.getByLabelText(/legal name/i)).toHaveValue("EQUINOR ASA");
    expect(screen.getByLabelText(/^legal id \*/i)).toHaveValue("923609016");
  });

  it("refreshes legal data from the registry by legal id", async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.startsWith("/api/v1/lookup/brreg?legalId=")) {
        return Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] }));
      }
      return Promise.resolve(jsonResponse(200, { data: [] }));
    });
    stubFetch(fetchMock);

    renderModal({
      mode: "edit",
      customer: {
        id: 1001,
        name: "Equinor",
        identity: { country: "no", type: "business", id: "923609016", name: "Old Name AS", source: "brreg" },
      },
    });

    await userEvent.click(screen.getByRole("button", { name: /refresh data/i }));

    await waitFor(() => {
      expect(screen.getByLabelText(/legal name/i)).toHaveValue("EQUINOR ASA");
    });
    expect(await screen.findByText("Registry data refreshed")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/lookup/brreg?legalId=923609016", { signal: undefined });
  });

  it("submits source brreg after picking a registry suggestion", async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.startsWith("/api/v1/lookup/brreg")) {
        return Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] }));
      }
      return Promise.resolve(jsonResponse(201, { id: 1001 }));
    });
    stubFetch(fetchMock);

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/^name/i), "Equinor");
    await userEvent.click(screen.getByRole("switch"));
    await userEvent.type(screen.getByLabelText(/legal name/i), "equinor");
    await userEvent.click(await screen.findByText("EQUINOR ASA"));
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Equinor",
        identity: { country: "no", type: "business", id: "923609016", name: "EQUINOR ASA", source: "brreg" },
      }),
    });
  });

  it("flips the source back to manual when registry data is edited by hand", async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.startsWith("/api/v1/lookup/brreg")) {
        return Promise.resolve(jsonResponse(200, { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] }));
      }
      return Promise.resolve(jsonResponse(201, { id: 1001 }));
    });
    stubFetch(fetchMock);

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/^name/i), "Equinor");
    await userEvent.click(screen.getByRole("switch"));
    await userEvent.type(screen.getByLabelText(/legal name/i), "equinor");
    await userEvent.click(await screen.findByText("EQUINOR ASA"));

    // Hand-editing the legal id after picking from the registry degrades the source.
    await userEvent.type(screen.getByLabelText(/^legal id \*/i), "1");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Equinor",
        identity: { country: "no", type: "business", id: "9236090161", name: "EQUINOR ASA", source: "manual" },
      }),
    });
  });

  it("maps nested identity server validation errors onto the right fields", async () => {
    stubFetch((url: RequestInfo | URL) => {
      const urlString = String(url);
      if (urlString.startsWith("/api/v1/lookup/brreg")) {
        return Promise.resolve(jsonResponse(200, { data: [] }));
      }
      return Promise.resolve(
        jsonResponse(400, {
          title: "Invalid customer",
          status: 400,
          errors: { "identity.id": ["A legal id cannot be longer than 50 characters"] },
        }),
      );
    });

    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/^name/i), "Acme");
    await userEvent.click(screen.getByRole("switch"));
    await userEvent.type(screen.getByLabelText(/legal name/i), "Acme AS");
    await userEvent.type(screen.getByLabelText(/^legal id \*/i), "923609016");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(await screen.findByText("A legal id cannot be longer than 50 characters")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});
