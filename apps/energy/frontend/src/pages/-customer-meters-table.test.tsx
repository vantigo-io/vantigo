import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CustomerMeteringPoint } from "../api/energy";
import { CustomerMetersTable } from "./-customer-meters-table";

const { navigateSpy } = vi.hoisted(() => ({ navigateSpy: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateSpy }));

const singleMeter: CustomerMeteringPoint[] = [
  {
    meteringPoint: {
      id: 1,
      gsrn: "123456789012345678",
      meterNumber: "M-1",
      address: { streetAddress: "Main Street 1", postalCode: "0001", city: "Oslo", countryCode: "NO" },
      priceArea: "NO1",
      connectionStatus: "Connected",
      createdAt: "2026-01-01T00:00:00.000Z",
      updatedAt: "2026-01-01T00:00:00.000Z",
    },
    supplyPeriods: [
      {
        id: 1,
        meteringPointId: 1,
        customerId: 1001,
        start: "2026-01-06T00:00:00.000Z",
        end: "2026-01-07T00:00:00.000Z",
        status: "Active",
      },
    ],
  },
];

describe("CustomerMetersTable", () => {
  afterEach(() => {
    navigateSpy.mockClear();
  });

  it("keeps supply period dates on their UTC calendar days", () => {
    render(
      <MantineProvider>
        <CustomerMetersTable meters={singleMeter} />
      </MantineProvider>,
    );

    expect(screen.getByText("Jan 6, 2026 – Jan 7, 2026")).toBeInTheDocument();
  });

  it("navigates to the bare metering-point path, not a tenant-prefixed one", () => {
    // Simulate being on a real (single-tenant) module route rather than jsdom's
    // default "/" — this is exactly the case that used to double up the segment
    // via window.location.pathname.split("/")[1] (e.g. "/energy/energy/...").
    window.history.pushState({}, "", "/energy/metering-points");

    render(
      <MantineProvider>
        <CustomerMetersTable meters={singleMeter} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByText("123456789012345678"));

    expect(navigateSpy).toHaveBeenCalledWith({ href: "/energy/metering-points/1" });
  });
});
