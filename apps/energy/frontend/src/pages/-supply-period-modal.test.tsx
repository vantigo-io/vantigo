import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { SupplyPeriodModal } from "./-supply-period-modal";

describe("SupplyPeriodModal", () => {
  it("surfaces switch problem details", async () => {
    const fetchMock = stubFetch(
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/customers?"))
          return Promise.resolve(
            new Response(JSON.stringify({ data: [{ id: 1002, name: "New customer" }], pagination: {} }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          );
        return Promise.resolve(
          new Response(JSON.stringify({ title: "Overlapping supply period", detail: "The period overlaps." }), {
            status: 409,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }),
    );
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <SupplyPeriodModal meteringPointId={1} opened onClose={vi.fn()} hasActivePeriod />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/customers?"))).toBe(true),
    );
    fireEvent.click(screen.getByRole("combobox", { name: "Customer" }));
    fireEvent.click(await screen.findByText("New customer"));
    fireEvent.change(document.querySelector('input[type="date"]') as HTMLInputElement, {
      target: { value: "2026-08-11" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Switch customer" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/energy/metering-points/1/supply-periods/switch",
        expect.anything(),
      ),
    );
    expect(await screen.findByText("The period overlaps.")).toBeInTheDocument();
  });

  const json = (body: unknown) =>
    new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

  const renderSwitch = (canViewTariffs: boolean) => {
    const fetchMock = stubFetch(async (input) => {
      const url = String(input);
      if (url.includes("/customers?")) return json({ data: [{ id: 1002, name: "New customer" }], pagination: {} });
      if (url.startsWith("/api/v1/energy/tariffs"))
        return json([{ id: 1001, kind: "power", name: "Spot 2026", status: "active" }]);
      return json({ endedPeriod: null, newPeriod: { id: 9 } });
    });
    const onClose = vi.fn();
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={new QueryClient()}>
          <SupplyPeriodModal meteringPointId={1} opened onClose={onClose} canViewTariffs={canViewTariffs} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    const body = () => {
      const call = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
      return call ? JSON.parse(String(call[1]?.body)) : null;
    };
    return { fetchMock, onClose, body };
  };

  const chooseCustomerAndDate = async () => {
    await userEvent.click(screen.getByRole("combobox", { name: "Customer" }));
    await userEvent.click(await screen.findByText("New customer"));
    fireEvent.change(document.querySelector('input[type="date"]') as HTMLInputElement, {
      target: { value: "2026-08-11" },
    });
  };

  it("starts the period on the power tariff and the cadence chosen", async () => {
    const { fetchMock, onClose, body } = renderSwitch(true);
    await chooseCustomerAndDate();
    expect(screen.getByText(/A consumer may be billed at most quarterly/)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /^Billing/ })).toHaveValue("Monthly");
    await userEvent.click(screen.getByRole("combobox", { name: /^Power tariff/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Spot 2026" }));
    await userEvent.click(screen.getByRole("combobox", { name: /^Billing/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Quarterly" }));
    await userEvent.click(screen.getByRole("button", { name: "Assign customer" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
      "/api/v1/energy/tariffs?kind=power&status=active",
    );
    expect(body()).toMatchObject({ customerId: 1002, tariffId: 1001, cadence: "quarterly" });
  });

  it("starts the period with no tariff and monthly without energy:tariffs-view, and lists none", async () => {
    const { fetchMock, onClose, body } = renderSwitch(false);
    await chooseCustomerAndDate();
    expect(screen.getByRole("textbox", { name: "Power tariff" })).toHaveValue("None");
    await userEvent.click(screen.getByRole("button", { name: "Assign customer" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/tariffs"))).toBe(false);
    expect(body()).toMatchObject({ tariffId: null, cadence: "monthly" });
  });
});
