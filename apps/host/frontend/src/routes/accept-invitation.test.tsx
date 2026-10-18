import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderRoute } from "../test/render-route";
import { AcceptInvitationPage } from "./accept-invitation";
import "../i18n";

const api = vi.hoisted(() => ({ validateInvitation: vi.fn(), acceptInvitation: vi.fn(), fetchSession: vi.fn() }));

vi.mock("../api/account-lifecycle", () => ({
  validateInvitation: api.validateInvitation,
  acceptInvitation: api.acceptInvitation,
}));

vi.mock("../api/auth", () => ({ fetchSession: api.fetchSession, sessionQueryKey: ["auth", "session"] }));

const renderAccept = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/accept-invitation", () => <AcceptInvitationPage token="tok-1" />, queryClient);
  await screen.findByText("Invitation for kari@example.com");
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  api.fetchSession.mockResolvedValue({
    user: { id: "u-1", displayName: "Kari", email: "kari@example.com", roles: [] },
  });
  api.acceptInvitation.mockResolvedValue({
    user: { id: "u-1", displayName: "Kari", email: "kari@example.com", roles: [] },
  });
});

describe("accepting an invitation", () => {
  it("asks a display name and a password for an ordinary invitation", async () => {
    api.validateInvitation.mockResolvedValue({ valid: true, email: "kari@example.com", role: "User" });
    await renderAccept();
    expect(screen.getByLabelText("Display name")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Kari N" } });
    fireEvent.change(screen.getByLabelText(/Password/), { target: { value: "Correct-horse-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));
    await waitFor(() =>
      expect(api.acceptInvitation).toHaveBeenCalledWith({
        token: "tok-1",
        displayName: "Kari N",
        password: "Correct-horse-1",
      }),
    );
  });

  it("asks only a password for an account Staff prepared, and says the account is ready", async () => {
    // Validate names no role for an invitation that completes an existing (Staff-managed) user.
    api.validateInvitation.mockResolvedValue({ valid: true, email: "kari@example.com" });
    await renderAccept();
    expect(screen.queryByLabelText("Display name")).not.toBeInTheDocument();
    expect(
      screen.getByText(
        "Your employer has set up this account for you, with your name and roles. Choose a password to start using it.",
      ),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Password/), { target: { value: "Correct-horse-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Activate account" }));
    await waitFor(() =>
      expect(api.acceptInvitation).toHaveBeenCalledWith({ token: "tok-1", password: "Correct-horse-1" }),
    );
  });
});
