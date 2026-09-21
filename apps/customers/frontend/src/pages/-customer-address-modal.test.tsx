import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { type AddressModalState, CustomerAddressModal } from "./-customer-address-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const address = (overrides: Record<string, unknown> = {}) => ({
  id: 1,
  type: "invoice",
  label: null,
  line1: "Storgata 1",
  line2: null,
  postalCode: "0150",
  city: "Oslo",
  region: null,
  country: "no",
  isPrimary: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...overrides,
});

const renderModal = (
  state: AddressModalState,
  addresses: ReturnType<typeof address>[] = [],
  fetchMock: ReturnType<typeof vi.fn> = vi.fn(),
) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const onClose = vi.fn();
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <CustomerAddressModal customerId={1001} addresses={addresses} state={state} onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { onClose, fetchMock };
};

describe("CustomerAddressModal", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    setLanguagePreference("auto");
  });

  it("offers the four address types", async () => {
    renderModal({ mode: "add" });
    await userEvent.click(screen.getByRole("combobox", { name: "Address type" }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual(["Invoice", "Postal", "Delivery", "Visiting"]);
  });

  it("defaults the country to Norway and hints at the postal code rule", () => {
    renderModal({ mode: "add" });
    expect(screen.getByRole("combobox", { name: "Country" })).toHaveValue("Norway");
    expect(screen.getByText("A Norwegian address needs a four-digit postal code and a city.")).toBeInTheDocument();
  });

  it("forces the primary checkbox on and disabled when it is the only address of its type", () => {
    renderModal({ mode: "add" }, []);
    const checkbox = screen.getByRole("checkbox", { name: "Primary invoice address" });
    expect(checkbox).toBeChecked();
    expect(checkbox).toBeDisabled();
  });

  it("leaves the primary checkbox free when another address of the type already exists", () => {
    renderModal({ mode: "add" }, [address({ id: 9, isPrimary: true })]);
    const checkbox = screen.getByRole("checkbox", { name: "Primary invoice address" });
    expect(checkbox).not.toBeChecked();
    expect(checkbox).not.toBeDisabled();
  });

  it("forces the primary checkbox on when editing the address that is currently primary", () => {
    const primary = address({ id: 1, isPrimary: true });
    renderModal({ mode: "edit", address: primary }, [primary, address({ id: 2, isPrimary: false })]);
    const checkbox = screen.getByRole("checkbox", { name: "Primary invoice address" });
    expect(checkbox).toBeChecked();
    expect(checkbox).toBeDisabled();
  });

  it("leaves the primary checkbox free when editing a non-primary address alongside a primary one", () => {
    const nonPrimary = address({ id: 2, isPrimary: false });
    renderModal({ mode: "edit", address: nonPrimary }, [address({ id: 1, isPrimary: true }), nonPrimary]);
    const checkbox = screen.getByRole("checkbox", { name: "Primary invoice address" });
    expect(checkbox).not.toBeChecked();
    expect(checkbox).not.toBeDisabled();
  });

  it("lower-cases the interpolated type in Norwegian, matching deleteAddressConfirm's own call site", async () => {
    // The type is a parenthetical, not a compound: nb reads "Primæradresse
    // (fakturaadresse)", never a word glued together through interpolation.
    setLanguagePreference("nb");
    renderModal({ mode: "add" }, []);
    expect(await screen.findByRole("checkbox", { name: "Primæradresse (fakturaadresse)" })).toBeInTheDocument();
  });

  it("drops the primary tick when the address is moved to a type that already has one", async () => {
    // "Primary" is per type: the invoice address's tick says nothing about
    // the postal group, where another address already holds it.
    const invoicePrimary = address({ id: 1, type: "invoice", isPrimary: true });
    renderModal({ mode: "edit", address: invoicePrimary }, [
      invoicePrimary,
      address({ id: 2, type: "invoice", isPrimary: false }),
      address({ id: 3, type: "postal", isPrimary: true }),
    ]);

    expect(screen.getByRole("checkbox", { name: "Primary invoice address" })).toBeChecked();

    await userEvent.click(screen.getByRole("combobox", { name: "Address type" }));
    await userEvent.click(await screen.findByRole("option", { name: "Postal" }));

    const checkbox = await screen.findByRole("checkbox", { name: "Primary postal address" });
    expect(checkbox).not.toBeChecked();
    expect(checkbox).not.toBeDisabled();
  });

  it("POSTs the full new address on add", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, address()));
    const { onClose } = renderModal({ mode: "add" }, [], fetchMock);

    await userEvent.type(screen.getByLabelText(/^Address line 1/), "Storgata 1");
    await userEvent.type(screen.getByLabelText("Postal code"), "0150");
    await userEvent.type(screen.getByLabelText("City"), "Oslo");
    await userEvent.click(screen.getByRole("button", { name: "Add address" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        type: "invoice",
        label: null,
        line1: "Storgata 1",
        line2: null,
        postalCode: "0150",
        city: "Oslo",
        region: null,
        country: "no",
        isPrimary: true,
      }),
    });
  });

  it("PUTs the full address on edit, to the address's own URL", async () => {
    const existing = address({ id: 7, label: "Head office", isPrimary: false });
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, existing));
    const { onClose } = renderModal(
      { mode: "edit", address: existing },
      [address({ id: 1, isPrimary: true }), existing],
      fetchMock,
    );

    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/7", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        type: "invoice",
        label: "Head office",
        line1: "Storgata 1",
        line2: null,
        postalCode: "0150",
        city: "Oslo",
        region: null,
        country: "no",
        isPrimary: false,
      }),
    });
  });

  it("maps 400 field errors, including isPrimary, onto the right inputs", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(400, {
        title: "Invalid address",
        status: 400,
        errors: {
          postalCode: ["A Norwegian address needs a four-digit postal code"],
          isPrimary: [
            "An address that is the only or primary one of its type stays primary; make another one primary instead",
          ],
        },
      }),
    );
    renderModal({ mode: "add" }, [address({ id: 9, isPrimary: true })], fetchMock);

    await userEvent.type(screen.getByLabelText(/^Address line 1/), "Storgata 1");
    await userEvent.click(screen.getByRole("button", { name: "Add address" }));

    expect(await screen.findByText("A Norwegian address needs a four-digit postal code")).toBeInTheDocument();
    expect(
      screen.getByText(
        "An address that is the only or primary one of its type stays primary; make another one primary instead",
      ),
    ).toBeInTheDocument();
  });

  it("shows the 50-address cap as a form-level alert, keyed 'addresses'", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(400, {
        title: "Invalid address",
        status: 400,
        errors: { addresses: ["A customer can have at most 50 addresses"] },
      }),
    );
    renderModal({ mode: "add" }, [], fetchMock);

    await userEvent.type(screen.getByLabelText(/^Address line 1/), "Storgata 1");
    await userEvent.click(screen.getByRole("button", { name: "Add address" }));

    expect(await screen.findByText("A customer can have at most 50 addresses")).toBeInTheDocument();
  });
});
