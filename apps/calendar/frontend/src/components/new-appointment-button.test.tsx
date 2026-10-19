import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import "../i18n";
import { renderRoute } from "../test/route-tree";
import { stubCalendarServer } from "../test/server";
import { NewAppointmentButton } from "./new-appointment-button";

describe("NewAppointmentButton", () => {
  it("opens the calendar's new appointment sheet with the customer", async () => {
    stubCalendarServer();
    const { router } = renderRoute("/", <NewAppointmentButton customerId={42} />);

    await userEvent.click(await screen.findByRole("button", { name: "New appointment" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/calendar"));
    expect(router.state.location.search).toMatchObject({ new: 1, customerId: 42 });
    expect(await screen.findByText("Customer: no. 42")).toBeInTheDocument();
  });

  it("carries the project", async () => {
    stubCalendarServer();
    const { router } = renderRoute("/", <NewAppointmentButton projectId={7} variant="light" />);

    await userEvent.click(await screen.findByRole("button", { name: "New appointment" }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ new: 1, projectId: 7 }));
    expect(router.state.location.search).not.toHaveProperty("customerId");
  });
});
