import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import type { CalibrationPoint, RoleCalibration } from "../api/types";
import { CalibrationPanel } from "./calibration-panel";

const mocks = vi.hoisted(() => ({ calibration: vi.fn() }));

vi.mock("../api/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/agents")>()),
  calibrationQueryOptions: (roleId: string) => ({
    queryKey: ["cal", roleId],
    queryFn: () => mocks.calibration(roleId),
  }),
}));

const points: CalibrationPoint[] = Array.from({ length: 51 }, (_, index) => ({
  x: 50 + index,
  autoCount: 100 - index,
  autoShare: (100 - index) / 100,
  badCount: index,
  badShare: index / 100,
}));

const renderPanel = (calibration: RoleCalibration) => {
  mocks.calibration.mockResolvedValue(calibration);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <CalibrationPanel roleId="r1" threshold={85} />
      </MantineProvider>
    </QueryClientProvider>,
  );
};

describe("the calibration panel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("says there is not enough history, with the count", async () => {
    renderPanel({
      roleId: "r1",
      labelled: 12,
      minimum: 50,
      notEnoughHistory: true,
      threshold: 85,
      heldCount: 0,
      points: [],
    });
    expect(await screen.findByText("Not enough history yet: 12 of 50 decided replies.")).toBeInTheDocument();
  });

  it("reads the figures at the role's threshold and draws both lines and the marker", async () => {
    renderPanel({
      roleId: "r1",
      labelled: 120,
      minimum: 50,
      notEnoughHistory: false,
      threshold: 85,
      heldCount: 9,
      points,
    });
    expect(await screen.findByTestId("calibration-auto")).toHaveTextContent(
      "65 replies (65 %) would have gone out on their own",
    );
    expect(screen.getByTestId("calibration-bad")).toHaveTextContent(
      "Of those, 35 (35 %) a person edited beyond 20 % or discarded",
    );
    expect(screen.getByTestId("calibration-held")).toHaveTextContent("9 were held by a rule whatever the score");
    const chart = screen.getByRole("img", { name: /share that would go out on its own/ });
    expect(chart.querySelectorAll("path")).toHaveLength(2);
    expect(screen.getByTestId("calibration-marker")).toBeInTheDocument();
  });
});
