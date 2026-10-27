import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { MeteringPoint } from "../api/energy";
import { stubFetch } from "../test/fetch";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const point: MeteringPoint = {
  id: 7,
  gsrn: "707057500000000007",
  meterNumber: "M-7",
  address: { streetAddress: "Storgata 1", postalCode: "9008", city: "Tromsø", countryCode: "NO" },
  priceArea: "NO4",
  connectionStatus: "Connected",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  gridTariffId: 1004,
  gridTariffName: "Old grid",
  household: true,
  electricityTaxClass: "reduced",
  vatTreatment: "exempt_household_north",
};

const activeGrid = [{ id: 1001, kind: "grid", name: "Grid A", status: "active" }];

const renderModal = (state: MeteringPointModalState, canViewTariffs: boolean) => {
  const fetchMock = stubFetch(async (input) =>
    String(input).startsWith("/api/v1/energy/tariffs") ? json(activeGrid) : json(point),
  );
  const onClose = vi.fn();
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MeteringPointFormModal state={state} onClose={onClose} canViewTariffs={canViewTariffs} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  const written = () => {
    const call = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT" || init?.method === "POST");
    return call ? JSON.parse(String(call[1]?.body)) : null;
  };
  return { fetchMock, onClose, written };
};

describe("MeteringPointFormModal", () => {
  it("rejects a GSRN that is not exactly 18 digits", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <MeteringPointFormModal state={{ mode: "create" }} onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    fireEvent.change(screen.getByRole("textbox", { name: /GSRN/ }), { target: { value: "123" } });
    fireEvent.click(screen.getByRole("button", { name: "Create metering point" }));
    expect(await screen.findByText("GSRN must contain exactly 18 digits")).toBeInTheDocument();
  });

  it("keeps the billing facts on an edit of the address: the archived grid tariff held, household and the tax class", async () => {
    const { fetchMock, onClose, written } = renderModal({ mode: "edit", meteringPoint: point }, true);
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
        "/api/v1/energy/tariffs?kind=grid&status=active",
      ),
    );
    expect(await screen.findByDisplayValue("Old grid (archived)")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: /^Household use/ })).toBeChecked();
    expect(screen.getByDisplayValue("Reduced rate")).toBeInTheDocument();
    const street = screen.getByRole("textbox", { name: "Street address" });
    await userEvent.clear(street);
    await userEvent.type(street, "Storgata 2");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(written()).toMatchObject({
      address: { streetAddress: "Storgata 2" },
      gridTariffId: 1004,
      household: true,
      electricityTaxClass: "reduced",
    });
  });

  it("refreshes the tariffs on a save, so their usage and Delete follow the grid tariff", async () => {
    const { fetchMock, onClose } = renderModal({ mode: "edit", meteringPoint: point }, true);
    const tariffReads = () =>
      fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/api/v1/energy/tariffs")).length;
    await waitFor(() => expect(tariffReads()).toBe(1));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    await waitFor(() => expect(tariffReads()).toBe(2));
  });

  it("sends the grid tariff chosen, and none once cleared", async () => {
    const { onClose, written } = renderModal({ mode: "edit", meteringPoint: point }, true);
    await userEvent.click(await screen.findByRole("combobox", { name: /^Grid tariff/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Grid A" }));
    await userEvent.click(screen.getByRole("checkbox", { name: /^Household use/ }));
    await userEvent.click(screen.getByRole("combobox", { name: "Electricity tax" }));
    await userEvent.click(await screen.findByRole("option", { name: "Exempt" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(written()).toMatchObject({ gridTariffId: 1001, household: false, electricityTaxClass: "exempt" });
  });

  it("shows the held grid tariff read-only without energy:tariffs-view, and sends it unchanged", async () => {
    const { fetchMock, onClose, written } = renderModal({ mode: "edit", meteringPoint: point }, false);
    expect(screen.getByRole("textbox", { name: "Grid tariff" })).toHaveValue("Old grid");
    expect(screen.getByRole("textbox", { name: "Grid tariff" })).toHaveAttribute("readonly");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/tariffs"))).toBe(false);
    expect(written()).toMatchObject({ gridTariffId: 1004, household: true, electricityTaxClass: "reduced" });
  });

  it("creates a point with no grid tariff, not household and the ordinary rate by default", async () => {
    const { onClose, written } = renderModal({ mode: "create" }, true);
    await userEvent.type(screen.getByRole("textbox", { name: /GSRN/ }), "707057500000000008");
    await userEvent.type(screen.getByRole("textbox", { name: "Meter number" }), "M-8");
    await userEvent.type(screen.getByRole("textbox", { name: "Street address" }), "Kongens gate 1");
    await userEvent.type(screen.getByRole("textbox", { name: "Postal code" }), "0153");
    await userEvent.type(screen.getByRole("textbox", { name: "City" }), "Oslo");
    await userEvent.click(screen.getByRole("button", { name: "Create metering point" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(written()).toMatchObject({ gridTariffId: null, household: false, electricityTaxClass: "ordinary" });
  });
});
