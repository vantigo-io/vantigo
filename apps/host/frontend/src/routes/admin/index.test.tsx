import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MaintenanceControls } from "./-maintenance-controls";
import "../../i18n";

const { fetchSystemStatus, setMaintenance } = vi.hoisted(() => ({
  fetchSystemStatus: vi.fn(),
  setMaintenance: vi.fn(),
}));

vi.mock("../../api/system-status", () => ({
  fetchSystemStatus,
  setMaintenance,
  systemStatusQueryKey: ["system", "status"],
}));

describe("admin maintenance controls", () => {
  beforeEach(() => {
    fetchSystemStatus.mockResolvedValue({ maintenance: false, message: null });
    setMaintenance.mockResolvedValue({ maintenance: true, message: "Deploying" });
  });

  it("saves the enabled state and plain-text message", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <MaintenanceControls />
        </QueryClientProvider>
      </MantineProvider>,
    );

    fireEvent.click(await screen.findByRole("switch", { name: "Enable maintenance mode" }));
    fireEvent.change(screen.getByLabelText("Message shown to users"), { target: { value: "Deploying" } });
    fireEvent.click(screen.getByRole("button", { name: "Save maintenance settings" }));

    await waitFor(() => expect(setMaintenance).toHaveBeenCalledWith({ enabled: true, message: "Deploying" }));
  });
});
