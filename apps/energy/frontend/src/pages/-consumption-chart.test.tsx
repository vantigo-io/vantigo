import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ConsumptionAggregate } from "../api/energy";
import { ConsumptionChart } from "./-consumption-chart";

vi.mock("@mantine/charts", () => ({
  AreaChart: ({ data, dataKey }: { data: Array<Record<string, unknown>>; dataKey: string }) => (
    <div data-testid="consumption-chart">{data.map((item) => String(item[dataKey])).join(",")}</div>
  ),
}));

describe("ConsumptionChart", () => {
  it("renders aggregate bucket labels", () => {
    const aggregates: ConsumptionAggregate[] = [
      {
        bucketStart: "2026-01-06T00:00:00.000Z",
        bucketEnd: "2026-01-07T00:00:00.000Z",
        quantityKwh: 12,
        intervalCount: 24,
        hasEstimated: false,
      },
    ];
    render(
      <MantineProvider>
        <ConsumptionChart aggregates={aggregates} resolution="day" />
      </MantineProvider>,
    );
    expect(screen.getByTestId("consumption-chart")).toHaveTextContent("Jan 6");
  });
});
