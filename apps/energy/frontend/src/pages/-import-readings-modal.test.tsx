import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ImportReadingsModal } from "./-import-readings-modal";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const result = (overrides: Record<string, unknown> = {}) => ({
  dryRun: true,
  evaluated: false,
  imported: false,
  encoding: "utf-8",
  separator: ";",
  header: ["Fra", "Til", "Forbruk", "Kvalitet"],
  sample: [{ row: 1, cells: ["01.01.2026 00:00", "01.01.2026 01:00", "1,5", "Målt"] }],
  rows: 4,
  suggested: { start: 0, end: 1, quantityKwh: 2, quality: 3 },
  new: 0,
  replacing: 0,
  unchanged: 0,
  errorCount: 0,
  errors: [],
  ...overrides,
});

/** The calls the modal made: dryRun and the mapping it sent, null when none. */
interface Call {
  dryRun: string | null;
  mapping: unknown;
}

const renderModal = (answer: (call: Call) => Response) => {
  const calls: Call[] = [];
  stubFetch(async (input, init) => {
    const url = new URL(String(input), "http://localhost");
    const body = init?.body as FormData;
    const mapping = body.get("mapping");
    const call = { dryRun: url.searchParams.get("dryRun"), mapping: mapping ? JSON.parse(String(mapping)) : null };
    calls.push(call);
    expect(url.pathname).toBe("/api/v1/energy/metering-points/7/consumption/import");
    expect(body.get("file")).toBeInstanceOf(File);
    return answer(call);
  });
  const onClose = vi.fn();
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
        <ImportReadingsModal meteringPointId={7} opened onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { calls, onClose };
};

const chooseFile = async (user: ReturnType<typeof userEvent.setup>) => {
  const input = document.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("no file input");
  await user.upload(input, new File(["Fra;Til;Forbruk;Kvalitet\n"], "readings.csv", { type: "text/csv" }));
};

describe("ImportReadingsModal", () => {
  it("reads the file, prefills the suggested columns, checks, and imports after confirming", async () => {
    const user = userEvent.setup();
    const { calls, onClose } = renderModal((call) => {
      if (!call.mapping) return json(result());
      if (call.dryRun === "true")
        return json(
          result({
            evaluated: true,
            new: 3,
            replacing: 1,
            unchanged: 2,
            rangeStart: "2025-12-31T23:00:00Z",
            rangeEnd: "2026-01-01T05:00:00Z",
          }),
        );
      return json(result({ evaluated: true, imported: true, dryRun: false, new: 3, replacing: 1, unchanged: 2 }));
    });

    await chooseFile(user);
    expect(await screen.findByText("4 rows")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Start" })).toHaveValue("Fra");
    expect(screen.getByRole("combobox", { name: "End" })).toHaveValue("Til");
    expect(screen.getByRole("combobox", { name: "Quantity (kWh)" })).toHaveValue("Forbruk");
    expect(screen.getByRole("combobox", { name: "Quality" })).toHaveValue("Kvalitet");
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Check" }));
    expect(await screen.findByText("3 new, 1 replacing, 2 unchanged")).toBeInTheDocument();
    expect(calls[1]).toEqual({ dryRun: "true", mapping: { start: 0, end: 1, quantityKwh: 2, quality: 3 } });

    await user.click(screen.getByRole("button", { name: "Import" }));
    const confirm = screen.getByText("Adds 3 readings and replaces 1.").closest("[role=alert]") as HTMLElement;
    await user.click(within(confirm).getByRole("button", { name: "Import" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls[2]).toEqual({ dryRun: "false", mapping: { start: 0, end: 1, quantityKwh: 2, quality: 3 } });
    expect(notifications.show).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Readings imported", message: "Added 3 readings and replaced 1." }),
    );
  });

  it("lists a check's errors and offers no import", async () => {
    const user = userEvent.setup();
    renderModal((call) =>
      call.mapping
        ? json(
            result({
              evaluated: true,
              new: 2,
              errorCount: 1,
              errors: [{ row: 3, column: 0, message: "29.03.2026 02:00 is not a time in Europe/Oslo" }],
            }),
          )
        : json(result({ suggested: { start: 0, quantityKwh: 2, resolution: "PT1H" } })),
    );

    await chooseFile(user);
    expect(await screen.findByRole("combobox", { name: "Resolution" })).toHaveValue("One hour (PT1H)");
    expect(screen.getByRole("combobox", { name: "End" })).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "Check" }));
    expect(await screen.findByText("29.03.2026 02:00 is not a time in Europe/Oslo")).toBeInTheDocument();
    expect(screen.getByText(/1 problems were found/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });
});
