import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "../i18n";
import { MfaEnrolmentNotice } from "./mfa-enrolment-notice";

describe("MfaEnrolmentNotice", () => {
  it("tells an administrator why they are here and exactly what to do", () => {
    render(
      <MantineProvider>
        <MfaEnrolmentNotice />
      </MantineProvider>,
    );
    expect(screen.getByRole("heading", { name: "Set up two-factor authentication to continue" })).toBeInTheDocument();
    // Why: the installation requires it for administrator accounts, and
    // everything else is closed until it is done.
    expect(screen.getByText(/requires two-factor authentication for administrator accounts/i)).toBeInTheDocument();
    expect(screen.getByText(/every other page will bring you back here/i)).toBeInTheDocument();
    // What: the steps, in the order the form below asks for them.
    const steps = screen.getAllByRole("listitem").map((item) => item.textContent);
    expect(steps).toHaveLength(5);
    expect(steps[0]).toMatch(/install an authenticator app/i);
    expect(steps[1]).toMatch(/current password/i);
    expect(steps[2]).toMatch(/setup URI/i);
    expect(steps[3]).toMatch(/six-digit code/i);
    expect(steps[4]).toMatch(/recovery codes/i);
    // After: access returns at once.
    expect(screen.getByText(/as soon as the authenticator is enabled/i)).toBeInTheDocument();
  });
});
