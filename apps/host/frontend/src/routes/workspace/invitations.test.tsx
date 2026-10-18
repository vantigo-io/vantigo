import { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Invitation } from "../../api/account-lifecycle";
import { renderRoute } from "../../test/render-route";
import { Route as InvitationsRoute } from "./invitations";
import "../../i18n";

const { listInvitations } = vi.hoisted(() => ({ listInvitations: vi.fn() }));

vi.mock("../../api/account-lifecycle", () => ({ listInvitations, invitationAction: vi.fn() }));

const invitation = (email: string, overrides: Partial<Invitation>): Invitation => ({
  id: email,
  email,
  role: "User",
  displayName: email.split("@")[0],
  createdAt: "2026-10-01T08:00:00Z",
  expiresAt: "2026-10-08T08:00:00Z",
  revokedAt: null,
  acceptedAt: null,
  ...overrides,
});

const reasons = [
  ["owner", "Revoked by an Owner"],
  ["replaced", "Replaced by a newer invitation or an account"],
  ["delivery_failed", "The email could not be sent"],
  ["email_changed", "The user's address changed"],
  ["deactivated", "Staff deactivated the account"],
] as const;

describe("the invitations page's revoked invitations", () => {
  it("says why each revoked invitation was revoked", async () => {
    listInvitations.mockResolvedValue(
      reasons.map(([reason]) =>
        invitation(`${reason}@example.com`, { revokedAt: "2026-10-02T08:00:00Z", revokedReason: reason }),
      ),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await renderRoute("/workspace/invitations", InvitationsRoute.options.component as never, queryClient);
    for (const [reason, words] of reasons) {
      const cell = (await screen.findAllByText(`${reason}@example.com`))[0];
      expect(within(cell.closest("tr") as HTMLElement).getByText(words)).toBeInTheDocument();
    }
  });

  it("names no reason for an invitation that is not revoked", async () => {
    listInvitations.mockResolvedValue([invitation("open@example.com", { expiresAt: "2999-01-01T00:00:00Z" })]);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await renderRoute("/workspace/invitations", InvitationsRoute.options.component as never, queryClient);
    const row = (await screen.findAllByText("open@example.com"))[0].closest("tr") as HTMLElement;
    for (const [, words] of reasons) expect(within(row).queryByText(words)).not.toBeInTheDocument();
  });
});
