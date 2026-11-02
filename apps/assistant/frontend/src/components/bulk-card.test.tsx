import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { bulkCard, bulkJob, stubAssistantApi } from "../test/server";
import { BulkCard, type BulkCardProps } from "./bulk-card";
import "../i18n";

const handlers = () => ({
  onStart: vi.fn(),
  onCancel: vi.fn(),
  onContinue: vi.fn(),
  onStop: vi.fn(),
});

const show = (card = bulkCard(), props: Partial<BulkCardProps> = {}) => {
  const fns = handlers();
  const result = renderWithProviders(<BulkCard card={card} {...fns} {...props} />);
  return { ...result, ...fns };
};

const jobUrl = "/api/v1/assistant/bulk-jobs/j1000000-0000-4000-8000-000000000001";

afterEach(() => {
  vi.useRealTimers();
});

describe("the bulk card", () => {
  it("previews: the count, a sample with now and new values, what is skipped, Start and Cancel", async () => {
    stubAssistantApi();
    const { onStart, onCancel } = show(
      bulkCard({
        excluded: {
          count: 5,
          reasons: [
            { code: "missing_id", count: 3 },
            { code: "duplicate", count: 2 },
          ],
        },
      }),
    );
    const group = screen.getByRole("group", { name: "Bulk change" });
    expect(within(group).getByText("Add the note to every project")).toBeInTheDocument();
    expect(within(group).getByTestId("bulk-found")).toHaveTextContent("Found 130 items. Nothing has been changed yet.");

    const sample = within(group).getByTestId("bulk-sample");
    expect(within(sample).getByRole("link", { name: "Bulk project 000" })).toHaveAttribute("href", "/projects/1");
    expect(within(sample).getByText("Old note")).toBeInTheDocument();
    expect(within(sample).getAllByText("Reviewed in bulk")).toHaveLength(2);

    const excluded = within(group).getByTestId("bulk-excluded");
    expect(excluded).toHaveTextContent("5 items will be skipped");
    expect(excluded).toHaveTextContent("3 carry no value to change them by");
    expect(excluded).toHaveTextContent("2 appear more than once in the list");
    expect(group).toHaveTextContent("Vantigo checks each record when its change runs");

    // It says what Start does, and never calls the preview a rehearsal.
    expect(group).toHaveTextContent("Start changes the first 5 items for real");
    expect(group.textContent?.toLowerCase()).not.toMatch(/dry run|test run|trial/);

    await userEvent.click(within(group).getByRole("button", { name: "Start" }));
    expect(onStart).toHaveBeenCalledOnce();
    await userEvent.click(within(group).getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("takes the focus when it arrives, never its Start button", () => {
    stubAssistantApi();
    show(bulkCard(), { autoFocus: true });
    expect(screen.getByRole("group", { name: "Bulk change" })).toHaveFocus();
    expect(screen.getByRole("button", { name: "Start" })).not.toHaveFocus();
  });

  it("shows the canary's outcomes and offers Continue or Stop", async () => {
    stubAssistantApi({
      handle: (method, path) =>
        method === "GET" && path === jobUrl
          ? jsonResponse(200, bulkJob({ state: "awaiting_continue", processed: 5, succeeded: 4, failed: 1 }))
          : undefined,
    });
    const canary = [0, 1, 2, 3, 4].map((seq) => ({
      seq,
      label: `Project ${seq}`,
      status: seq === 3 ? ("failed" as const) : ("succeeded" as const),
      title: seq === 3 ? "You may not change this" : undefined,
    }));
    const { onContinue, onStop } = show(
      bulkCard({
        state: "awaiting_continue",
        processed: 5,
        succeeded: 4,
        failed: 1,
        canary,
        pauseReason: "canary_done",
      }),
    );
    const table = screen.getByTestId("bulk-canary");
    expect(within(table).getAllByText("Changed")).toHaveLength(4);
    expect(within(table).getByText("Failed")).toBeInTheDocument();
    expect(within(table).getByText("You may not change this")).toBeInTheDocument();
    expect(screen.getByText("The first 5 are done")).toBeInTheDocument();
    expect(screen.getByText(/The other 125 are changed in the background, 100 at a time/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onContinue).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledOnce();
  });

  it("shows progress with a bar and the counters, polls every two seconds and stops polling when the job ends", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let polls = 0;
    const answers = [
      bulkJob({ state: "running", processed: 40, succeeded: 38, failed: 2, skipped: 0 }),
      bulkJob({ state: "running", processed: 100, succeeded: 98, failed: 2 }),
      bulkJob({
        state: "done",
        processed: 130,
        succeeded: 128,
        failed: 2,
        failures: [
          { seq: 7, label: "Project 7", status: "failed", httpStatus: 403, code: "forbidden", title: "No access" },
          { seq: 9, label: "Project 9", status: "failed", httpStatus: 409, code: "stale", title: "Changed meanwhile" },
        ],
      }),
    ];
    const fetchMock = stubAssistantApi({
      handle: (method, path) => {
        if (method === "GET" && path === jobUrl)
          return jsonResponse(200, answers[Math.min(polls++, answers.length - 1)]);
        return undefined;
      },
    });
    const { onStop } = show(bulkCard({ state: "running", processed: 10, succeeded: 10 }));

    const progress = await screen.findByTestId("bulk-progress");
    expect(progress).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("40 of 130")).toBeInTheDocument());
    expect(screen.getByText("Failed: 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop" })).toBeEnabled();
    expect(screen.getByText(/Running on the server/)).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(2100);
    await waitFor(() => expect(screen.getByText("100 of 130")).toBeInTheDocument());
    await vi.advanceTimersByTimeAsync(2100);
    await waitFor(() =>
      expect(screen.getByTestId("bulk-summary")).toHaveTextContent("Done: 128 changed, 2 failed, 0 skipped."),
    );

    // At the end it stops asking.
    const asked = polls;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(polls).toBe(asked);
    expect(sentAll(fetchMock, "GET").filter((call) => call.url === jobUrl)).toHaveLength(asked);

    const failures = screen.getByTestId("bulk-failures");
    expect(within(failures).getByText("Project 7")).toBeInTheDocument();
    expect(within(failures).getByText("403")).toBeInTheDocument();
    expect(within(failures).getByText("Changed meanwhile")).toBeInTheDocument();
    expect(onStop).not.toHaveBeenCalled();
  });

  it("stops: Stop asks, and a job that is stopping says so and cannot be stopped twice", async () => {
    stubAssistantApi({ handle: () => undefined });
    const { onStop, rerenderWithProviders } = show(bulkCard({ state: "running", processed: 10, succeeded: 10 }));
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledOnce();

    rerenderWithProviders(
      <BulkCard card={bulkCard({ state: "stopping", processed: 10, succeeded: 10 })} {...handlers()} />,
    );
    expect(await screen.findByText("Stopping after this chunk of 100")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
  });

  it("pauses for Continue when the assistant's permission was about to run out", async () => {
    stubAssistantApi({
      handle: (method, path) =>
        method === "GET" && path === jobUrl
          ? jsonResponse(
              200,
              bulkJob({ state: "awaiting_continue", processed: 100, succeeded: 100, pauseReason: "grant_expiring" }),
            )
          : undefined,
    });
    const { onContinue } = show(
      bulkCard({ state: "awaiting_continue", processed: 100, succeeded: 100, pauseReason: "grant_expiring" }),
    );
    expect(screen.getByText(/Confirm to go on/)).toBeInTheDocument();
    expect(screen.queryByText("The first 5 are done")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onContinue).toHaveBeenCalledOnce();
  });

  it("ends with a summary, the failed items, why a stopped job stopped and a CSV download", async () => {
    stubAssistantApi({
      handle: (method, path) =>
        method === "GET" && path === jobUrl
          ? jsonResponse(
              200,
              bulkJob({
                state: "stopped",
                errorCode: "session_ended",
                processed: 60,
                succeeded: 55,
                failed: 120,
                total: 130,
                failures: Array.from({ length: 50 }, (_, seq) => ({
                  seq,
                  label: `Project ${seq}`,
                  status: "failed" as const,
                  httpStatus: 403,
                  title: "No access",
                })),
              }),
            )
          : undefined,
    });
    show(
      bulkCard({ state: "stopped", errorCode: "session_ended", processed: 60, succeeded: 55, failed: 120, total: 130 }),
    );
    expect(screen.getByTestId("bulk-summary")).toHaveTextContent("Stopped after 60 of 130: 55 changed, 120 failed.");
    expect(screen.getByText("You signed out, so it stopped.")).toBeInTheDocument();
    const link = await screen.findByTestId("bulk-csv");
    expect(link).toHaveAttribute("href", `${jobUrl}/failures.csv`);
    expect(link).toHaveAttribute("download");
    expect(link).toHaveTextContent("Download failed items (CSV)");
    await waitFor(() => expect(screen.getByText("Showing the first 50 of 120.")).toBeInTheDocument());
    expect(within(screen.getByTestId("bulk-failures")).getAllByRole("row")).toHaveLength(51);
    // Nothing to stop or continue any more.
    expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
  });

  it("offers no download when nothing failed", async () => {
    stubAssistantApi({
      handle: (method, path) =>
        method === "GET" && path === jobUrl
          ? jsonResponse(200, bulkJob({ state: "done", processed: 130, succeeded: 130 }))
          : undefined,
    });
    show(bulkCard({ state: "done", processed: 130, succeeded: 130 }));
    expect(screen.getByTestId("bulk-summary")).toHaveTextContent("Done: 130 changed, 0 failed, 0 skipped.");
    expect(screen.queryByTestId("bulk-csv")).not.toBeInTheDocument();
  });

  it("reads cancelled and expired plans without buttons", () => {
    stubAssistantApi();
    const { unmount } = show(bulkCard({ state: "stopped", errorCode: "cancelled" }, { status: "cancelled" }));
    expect(screen.getAllByText("Cancelled").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    unmount();
    show(bulkCard({ state: "expired" }, { status: "expired" }));
    expect(screen.getAllByText("Expired").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
