import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ComposePage } from "./messages.compose";

const navigate = vi.fn();
const createMessage = vi.fn().mockResolvedValue({ messageId: "m-9", status: "queued", idempotencyKey: "key-9" });
let mailboxResponse = [
  {
    id: "box-default",
    fromAddress: "default@example.com",
    displayName: "Default",
    createdAt: "2026-01-01",
    isActive: true,
    provider: "smtp",
    isDefault: true,
    hasCredentials: true,
    settings: { host: "smtp.example.com", port: 587, useSsl: true },
  },
  {
    id: "box-other",
    fromAddress: "other@example.com",
    displayName: null,
    createdAt: "2026-01-02",
    isActive: true,
    provider: "mailgun",
    isDefault: false,
    hasCredentials: true,
    settings: { domain: "example.com", region: "us" },
  },
];

vi.mock("../api/messages", () => ({ createMessage: (...args: unknown[]) => createMessage(...args) }));
vi.mock("../api/auth", () => ({
  fetchSession: () =>
    Promise.resolve({ user: { id: "user-1", displayName: "Owner", email: "owner@example.com", roles: ["Owner"] } }),
  sessionQueryKey: ["auth", "session"],
}));
vi.mock("../api/mailboxes", () => ({
  mailboxesQueryOptions: () => ({ queryKey: ["mailboxes"], queryFn: () => Promise.resolve(mailboxResponse) }),
}));
vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: unknown }) => options,
  useNavigate: () => navigate,
}));
vi.mock("@tiptap/react", () => ({ useEditor: () => ({ getText: () => "Body", getHTML: () => "<p>Body</p>" }) }));
vi.mock("@tiptap/starter-kit", () => ({ default: { configure: () => ({}) } }));
vi.mock("@mantine/tiptap", () => {
  const Part = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const Editor = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  Editor.Toolbar = Part;
  Editor.ControlsGroup = Part;
  Editor.Content = Part;
  Editor.Bold = Part;
  Editor.Italic = Part;
  Editor.BulletList = Part;
  Editor.OrderedList = Part;
  Editor.Link = Part;
  Editor.Unlink = Part;
  return { RichTextEditor: Editor };
});

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient()}>
        <ComposePage />
      </QueryClientProvider>
    </MantineProvider>,
  );

afterEach(() => {
  cleanup();
  mailboxResponse = mailboxResponse.slice(0, 2);
  createMessage.mockClear();
  navigate.mockClear();
});

describe("message compose", () => {
  it("validates an empty subject and recipient list", async () => {
    renderPage();
    fireEvent.submit(screen.getByRole("button", { name: "Send message" }).closest("form") as HTMLFormElement);
    expect(await screen.findByText("Subject is required")).toBeTruthy();
    expect(screen.getByText("At least one recipient is required")).toBeTruthy();
    expect(createMessage).not.toHaveBeenCalled();
  });

  it("submits with an idempotency key and navigates to the message", async () => {
    renderPage();
    await screen.findByRole("combobox", { name: "Send from" });
    fireEvent.change(screen.getAllByPlaceholderText("Subject")[0], { target: { value: "Hello" } });
    const recipient = screen.getAllByPlaceholderText("Add recipient")[0];
    fireEvent.change(recipient, { target: { value: "person@example.com" } });
    fireEvent.keyDown(recipient, { key: "Enter", code: "Enter" });
    fireEvent.submit(screen.getByRole("button", { name: "Send message" }).closest("form") as HTMLFormElement);
    await waitFor(() => expect(createMessage).toHaveBeenCalled());
    expect(createMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        mailboxId: "box-default",
        subject: "Hello",
        to: [{ email: "person@example.com" }],
        textBody: "Body",
        htmlBody: "<p>Body</p>",
      }),
      expect.any(String),
    );
    expect(navigate).toHaveBeenCalledWith({ to: "/messages/$messageId", params: { messageId: "m-9" } });
  });

  it("shows the mailbox selector for an owner with multiple active mailboxes", async () => {
    renderPage();
    const selector = await screen.findByRole("combobox", { name: "Send from" });
    expect((selector as HTMLInputElement).value).toBe("Default <default@example.com>");
  });

  it("hides the mailbox selector when there is only one active mailbox", async () => {
    mailboxResponse = [mailboxResponse[0]];
    renderPage();
    await waitFor(() => expect(screen.queryByRole("combobox", { name: "Send from" })).toBeNull());
  });
});
