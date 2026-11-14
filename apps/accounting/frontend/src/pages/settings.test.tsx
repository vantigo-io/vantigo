import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { monthEndOf } from "../lib/lock";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubAccountingApi } from "../test/server";

describe("the settings", () => {
  it("knows a month's last day", () => {
    expect(monthEndOf("2026-02")).toBe("2026-02-28");
    expect(monthEndOf("2028-02")).toBe("2028-02-29");
    expect(monthEndOf("2026-12")).toBe("2026-12-31");
    expect(monthEndOf("")).toBeNull();
  });

  it("moves the lock back only with a reason", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/settings");
    expect(await screen.findByText(/locked through/)).toBeInTheDocument();
    expect(screen.getByText("Bimonthly")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/Lock through the end of/), "2026-07");
    const back = screen.getByRole("button", { name: "Move the lock back" });
    expect(back).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/Reason/), "Locked too early");
    await userEvent.click(back);
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/lock$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/lock$/)[0].body).toEqual({
      lockedThrough: "2026-07-31",
      reason: "Locked too early",
    });
  });
});
