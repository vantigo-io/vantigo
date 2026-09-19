import { createEvent, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../test/render";
import { HoursCell } from "./hours-cell";

/**
 * A read-only cell that leads to the day view (several entries, or one worked
 * between clock times) is otherwise only reachable by clicking it — the
 * day-column heading link is the other way in, but the cell's own affordance
 * should not be mouse-only.
 */
describe("HoursCell", () => {
  it("activates a read-only cell with somewhere to send it on Enter and on Space, from the keyboard alone", async () => {
    const onActivate = vi.fn();
    renderWithProviders(
      <HoursCell
        label="KVEM1000 › PM on Friday"
        hours={5}
        readOnly
        onCommit={() => {}}
        onInvalid={() => {}}
        onActivate={onActivate}
      />,
    );

    const cell = screen.getByRole("textbox", { name: "KVEM1000 › PM on Friday" });
    cell.focus();
    await userEvent.keyboard("{Enter}");
    expect(onActivate).toHaveBeenCalledTimes(1);

    const spaceEvent = createEvent.keyDown(cell, { key: " " });
    fireEvent(cell, spaceEvent);
    expect(onActivate).toHaveBeenCalledTimes(2);
    // Space would otherwise scroll the page — the cell must own the key.
    expect(spaceEvent.defaultPrevented).toBe(true);
  });

  it("is reachable by keyboard: a read-only cell with nowhere to send it never calls onActivate", async () => {
    const onCommit = vi.fn();
    renderWithProviders(<HoursCell label="Locked day" hours={5} readOnly onCommit={onCommit} onInvalid={() => {}} />);

    const cell = screen.getByRole("textbox", { name: "Locked day" });
    cell.focus();
    await userEvent.keyboard("{Enter} ");

    expect(onCommit).not.toHaveBeenCalled();
  });

  it("still saves an editable cell on Enter rather than treating it as an activation", async () => {
    const onCommit = vi.fn();
    const onActivate = vi.fn();
    renderWithProviders(
      <HoursCell label="Open day" hours={undefined} onCommit={onCommit} onInvalid={() => {}} onActivate={onActivate} />,
    );

    const cell = screen.getByRole("textbox", { name: "Open day" });
    await userEvent.type(cell, "4{Enter}");

    expect(onActivate).not.toHaveBeenCalled();
    expect(onCommit).toHaveBeenCalledWith(4);
  });
});
