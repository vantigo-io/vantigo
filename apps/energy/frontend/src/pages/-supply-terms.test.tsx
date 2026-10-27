import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SupplyPeriod } from "../api/energy";
import { stubFetch } from "../test/fetch";
import { SupplyTermsModal } from "./-supply-terms";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const period: SupplyPeriod = {
  id: 5,
  meteringPointId: 1,
  customerId: 1002,
  start: "2026-01-01T00:00:00Z",
  end: null,
  status: "Active",
  tariffId: 1003,
  tariffName: "Fixed 2025",
  cadence: "quarterly",
};

const renderTerms = (canViewTariffs: boolean, answer?: (init?: RequestInit) => Response | undefined) => {
  const fetchMock = stubFetch(async (input, init) => {
    if (String(input).startsWith("/api/v1/energy/tariffs"))
      return json([{ id: 1001, kind: "power", name: "Spot 2026", status: "active" }]);
    return answer?.(init) ?? json(period);
  });
  const onClose = vi.fn();
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient()}>
        <SupplyTermsModal meteringPointId={1} period={period} onClose={onClose} canViewTariffs={canViewTariffs} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  const put = () => {
    const call = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    return call ? { url: String(call[0]), body: JSON.parse(String(call[1]?.body)) } : null;
  };
  return { fetchMock, onClose, put };
};

describe("SupplyTermsModal", () => {
  it("opens on the period's terms, keeps the archived tariff it holds, and puts the change", async () => {
    const { onClose, put } = renderTerms(true);
    expect(await screen.findByDisplayValue("Fixed 2025 (archived)")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /^Billing/ })).toHaveValue("Quarterly");
    await userEvent.click(screen.getByRole("combobox", { name: /^Billing/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Yearly" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(put()).toEqual({
      url: "/api/v1/energy/metering-points/1/supply-periods/5",
      body: { tariffId: 1003, cadence: "yearly" },
    });
  });

  it("changes to another active tariff", async () => {
    const { onClose, put } = renderTerms(true);
    await userEvent.click(await screen.findByRole("combobox", { name: /^Power tariff/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Spot 2026" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(put()?.body).toEqual({ tariffId: 1001, cadence: "quarterly" });
  });

  it("keeps the tariff read-only and unchanged without energy:tariffs-view", async () => {
    const { fetchMock, onClose, put } = renderTerms(false);
    expect(screen.getByRole("textbox", { name: "Power tariff" })).toHaveValue("Fixed 2025");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/tariffs"))).toBe(false);
    expect(put()?.body).toEqual({ tariffId: 1003, cadence: "quarterly" });
  });

  it("shows the server's refusal on the field it names", async () => {
    const { onClose } = renderTerms(true, (init) =>
      init?.method === "PUT"
        ? json({ title: "Validation failed", errors: { tariffId: ["Tariff 1003 is archived."] } }, 400)
        : undefined,
    );
    await screen.findByDisplayValue("Fixed 2025 (archived)");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Tariff 1003 is archived.")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("shows a refusal that names no field in an alert", async () => {
    const { onClose } = renderTerms(true, (init) =>
      init?.method === "PUT"
        ? json({ title: "Invalid supply period", detail: "A cancelled period cannot be changed.", status: 400 }, 400)
        : undefined,
    );
    await screen.findByDisplayValue("Fixed 2025 (archived)");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("A cancelled period cannot be changed.");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("shows a refusal on a field the dialog does not hold in an alert", async () => {
    const { onClose } = renderTerms(true, (init) =>
      init?.method === "PUT"
        ? json({ title: "Invalid supply period", errors: { period: ["The period has changed."] } }, 400)
        : undefined,
    );
    await screen.findByDisplayValue("Fixed 2025 (archived)");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The period has changed.");
    expect(onClose).not.toHaveBeenCalled();
  });
});
