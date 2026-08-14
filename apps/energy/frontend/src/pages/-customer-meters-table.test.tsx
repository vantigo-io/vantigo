import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CustomerMeteringPoint } from "../api/energy";
import { CustomerMetersTable } from "./-customer-meters-table";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

describe("CustomerMetersTable", () => {
  it("keeps supply period dates on their UTC calendar days", () => {
    const meters: CustomerMeteringPoint[] = [
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

    render(
      <MantineProvider>
        <CustomerMetersTable meters={meters} />
      </MantineProvider>,
    );

    expect(screen.getByText("Jan 6, 2026 – Jan 7, 2026")).toBeInTheDocument();
  });
});
