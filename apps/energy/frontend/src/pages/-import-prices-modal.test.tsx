import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ImportPricesModal } from "./-import-prices-modal";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const json = (body: unknown, status = 200, contentType = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": contentType } });

const result = (overrides: Record<string, unknown> = {}) => ({
  dryRun: true,
  imported: false,
  rows: 48,
  errorCount: 0,
  errors: [],
  areas: [
    {
      priceArea: "NO1",
      inserted: 20,
      replaced: 4,
      unchanged: 0,
      rangeStart: "2026-01-01T23:00:00Z",
      rangeEnd: "2026-01-02T23:00:00Z",
    },
    { priceArea: "NO2", inserted: 0, replaced: 0, unchanged: 24 },
  ],
  ...overrides,
});

const renderModal = (answer: (dryRun: string | null, call: number) => Response) => {
  const calls: (string | null)[] = [];
  stubFetch(async (input, init) => {
    const url = new URL(String(input), "http://localhost");
    expect(url.pathname).toBe("/api/v1/energy/spot-prices/import");
    expect(init?.method).toBe("POST");
    const body = init?.body as FormData;
    expect(body.get("file")).toBeInstanceOf(File);
    const dryRun = url.searchParams.get("dryRun");
    calls.push(dryRun);
    return answer(dryRun, calls.length);
  });
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const onClose = vi.fn();
  render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <ImportPricesModal opened onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { calls, invalidate, onClose };
};

const chooseFile = async (user: ReturnType<typeof userEvent.setup>) => {
  const input = document.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("no file input");
  await user.upload(input, new File(["area;start;nok_per_kwh\n"], "prices.csv", { type: "text/csv" }));
};

describe("ImportPricesModal", () => {
  it("checks the file as soon as it is chosen, imports it, and reads the prices again", async () => {
    const user = userEvent.setup();
    const { calls, invalidate, onClose } = renderModal((dryRun) =>
      dryRun === "true" ? json(result()) : json(result({ dryRun: false, imported: true })),
    );

    await chooseFile(user);
    expect(await screen.findByText("Dry run: 48 rows")).toBeInTheDocument();
    expect(calls).toEqual(["true"]);
    const no1 = screen.getByRole("row", { name: /NO1/ });
    expect(
      within(no1)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual(["NO1", "20", "4", "0", "Jan 2, 2026, 12:00 AM to Jan 3, 2026, 12:00 AM"]);
    expect(within(screen.getByRole("row", { name: /NO2/ })).getAllByRole("cell")[4]).toHaveTextContent("—");

    await user.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByText("Prices imported")).toBeInTheDocument();
    expect(screen.getByText("48 rows imported.")).toBeInTheDocument();
    expect(calls).toEqual(["true", "false"]);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["energy", "spot-prices"] });

    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("lists the errors of a dry run and offers no import", async () => {
    const user = userEvent.setup();
    renderModal(() =>
      json(
        result({
          errorCount: 250,
          errors: [
            { row: 3, column: 2, message: "the price is not a number" },
            { row: 7, message: "the interval overlaps a stored interval of another length" },
          ],
        }),
      ),
    );

    await chooseFile(user);
    expect(
      await screen.findByText(/250 problems were found\. Nothing can be imported until the file is fixed\./),
    ).toBeInTheDocument();
    expect(screen.getByText(/The first 2 are listed\./)).toBeInTheDocument();
    const row = screen.getByRole("row", { name: /the price is not a number/ });
    expect(
      within(row)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual(["3", "3", "the price is not a number"]);
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("offers no import when every price is already stored", async () => {
    const user = userEvent.setup();
    renderModal(() => json(result({ areas: [{ priceArea: "NO1", inserted: 0, replaced: 0, unchanged: 24 }] })));

    await chooseFile(user);
    expect(await screen.findByText("Every price in the file is already stored.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("checks again when the run answers 422, so the new errors are listed", async () => {
    const user = userEvent.setup();
    const { calls } = renderModal((dryRun, call) => {
      if (dryRun === "false") return json(result({ dryRun: false, errorCount: 1 }), 422);
      return call === 1
        ? json(result())
        : json(result({ errorCount: 1, errors: [{ row: 9, message: "the interval overlaps a stored interval" }] }));
    });

    await chooseFile(user);
    await user.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByText("the interval overlaps a stored interval")).toBeInTheDocument();
    expect(calls).toEqual(["true", "false", "true"]);
    expect(notifications.show).toHaveBeenCalledWith(
      expect.objectContaining({ message: "The file has errors now. They are listed below." }),
    );
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("shows the server's words when it refuses the file itself", async () => {
    const user = userEvent.setup();
    renderModal(() =>
      json(
        {
          title: "One or more validation errors occurred.",
          status: 400,
          errors: { file: ["The header has no price area column."] },
        },
        400,
        "application/problem+json",
      ),
    );

    await chooseFile(user);
    await vi.waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "The file could not be checked",
          message: "The header has no price area column.",
        }),
      ),
    );
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });
});
