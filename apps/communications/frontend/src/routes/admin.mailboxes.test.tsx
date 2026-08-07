import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MailboxesPage } from "./admin.mailboxes";

const createMailbox = vi.fn().mockResolvedValue({});
const updateMailbox = vi.fn().mockResolvedValue({});
const verifyMailbox = vi.fn().mockResolvedValue({ ok: true });
const mailboxes = [
  {
    id: "box-1",
    fromAddress: "one@example.com",
    displayName: "One",
    createdAt: "2026-01-01",
    isActive: true,
    provider: "smtp",
    isDefault: true,
    hasCredentials: true,
    settings: { host: "smtp.example.com", port: 587, useSsl: true },
  },
  {
    id: "box-2",
    fromAddress: "two@example.com",
    displayName: null,
    createdAt: "2026-01-02",
    isActive: true,
    provider: "smtp",
    isDefault: false,
    hasCredentials: true,
    settings: { host: "smtp.example.com", port: 587, useSsl: true },
  },
];

vi.mock("../api/auth", () => ({
  fetchSession: () =>
    Promise.resolve({ user: { id: "owner-1", displayName: "Owner", email: "owner@example.com", roles: ["Owner"] } }),
  sessionQueryKey: ["auth", "session"],
}));
vi.mock("../api/mailboxes", () => ({
  createMailbox: (...args: unknown[]) => createMailbox(...args),
  mailboxesQueryOptions: () => ({ queryKey: ["mailboxes"], queryFn: () => Promise.resolve(mailboxes) }),
  updateMailbox: (...args: unknown[]) => updateMailbox(...args),
  verifyMailbox: (...args: unknown[]) => verifyMailbox(...args),
}));
vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: unknown) => options,
  redirect: vi.fn(),
}));

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient()}>
        <MailboxesPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => {
  cleanup();
  createMailbox.mockClear();
  updateMailbox.mockClear();
  verifyMailbox.mockClear();
});

describe("admin mailboxes", () => {
  it("sets a non-default mailbox as the default", async () => {
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Set default" }));
    await waitFor(() => expect(updateMailbox).toHaveBeenCalledWith("box-2", { isDefault: true }));
  });
});
