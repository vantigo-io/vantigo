import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
});
