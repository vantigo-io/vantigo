import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CustomerAttachment } from "../api/attachments";
import { stubFetch } from "../test/fetch";
import { CustomerAttachmentsCard } from "./-customer-attachments-card";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

import { notifications } from "@mantine/notifications";

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const attachment = (overrides: Partial<CustomerAttachment> = {}): CustomerAttachment => ({
  id: 1001,
  customerId: 42,
  fileName: "Rammeavtale.pdf",
  contentType: "application/pdf",
  sizeBytes: 123456,
  uploadedAt: "2026-10-12T10:00:00Z",
  timelineEntry: null,
  ...overrides,
});

const renderCard = (fetchMock: ReturnType<typeof vi.fn>, canManageTimeline = true) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerAttachmentsCard customerId={42} canManageTimeline={canManageTimeline} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

/** The list answered once, then whatever `after` says for every other request. */
const listThen = (
  data: unknown[],
  after: (input: RequestInfo | URL, init?: RequestInit) => Response | Promise<Response>,
) =>
  vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && String(input).endsWith("/attachments"))
      return Promise.resolve(json(200, { data }));
    return Promise.resolve(after(input, init));
  });

describe("CustomerAttachmentsCard", () => {
  it("lists the attachments with their size, day and the entry each belongs to", async () => {
    renderCard(
      vi.fn().mockResolvedValue(
        json(200, {
          data: [
            attachment({
              id: 1002,
              fileName: "epost.pdf",
              sizeBytes: 2048,
              timelineEntry: {
                id: 7,
                eventType: "interaction.email",
                occurredOn: "2026-07-27",
                summary: "Kari sent the contract",
                state: "active",
              },
            }),
            attachment(),
            attachment({
              id: 1003,
              fileName: "gone.pdf",
              timelineEntry: { id: 8, eventType: "note", occurredOn: "2026-07-28", summary: "x", state: "deleted" },
            }),
          ],
        }),
      ),
    );
    expect(await screen.findByRole("button", { name: "Download epost.pdf" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download Rammeavtale.pdf" })).toBeInTheDocument();
    expect(screen.getByText("2 kB")).toBeInTheDocument();
    expect(screen.getAllByText("121 kB")).toHaveLength(2);
    expect(screen.getByText(/On: Email/)).toBeInTheDocument();
    expect(screen.getByText(/On a deleted entry: Note/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Delete / })).toHaveLength(3);
  });

  it("shows the empty state with Add attachment, and neither action without canManageTimeline", async () => {
    renderCard(vi.fn().mockResolvedValue(json(200, { data: [attachment()] })), false);
    expect(await screen.findByRole("button", { name: "Download Rammeavtale.pdf" })).toBeInTheDocument();
    expect(screen.queryByText("Add attachment")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Delete / })).not.toBeInTheDocument();
  });

  it("says the attachments could not be loaded rather than claiming there are none", async () => {
    renderCard(vi.fn().mockResolvedValue(new Response(null, { status: 403 })));
    expect(await screen.findByText("Could not load attachments.")).toBeInTheDocument();
    expect(screen.queryByText("No attachments yet")).not.toBeInTheDocument();
  });

  it("uploads a picked file as a multipart part named file and refreshes the list", async () => {
    let uploaded = false;
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        uploaded = true;
        expect(init.body).toBeInstanceOf(FormData);
        const file = (init.body as FormData).get("file") as File;
        expect(file.name).toBe("avtale.pdf");
        expect((init.body as FormData).get("timelineEntryId")).toBeNull();
        return Promise.resolve(json(201, attachment({ id: 1009, fileName: "avtale.pdf" })));
      }
      return Promise.resolve(json(200, { data: uploaded ? [attachment({ id: 1009, fileName: "avtale.pdf" })] : [] }));
    });
    renderCard(fetchMock);
    expect(await screen.findByText("No attachments yet")).toBeInTheDocument();
    const input = screen
      .getByText("Add attachment")
      .closest("button")
      ?.parentElement?.querySelector("input[type=file]");
    expect(input).not.toBeNull();
    await userEvent.upload(
      input as HTMLInputElement,
      new File([new Uint8Array(16)], "avtale.pdf", { type: "application/pdf" }),
    );
    expect(await screen.findByRole("button", { name: "Download avtale.pdf" })).toBeInTheDocument();
    expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "Attachment added" }));
  });

  it("shows the server's own words for a refused file and for the cap", async () => {
    let calls = 0;
    const fetchMock = listThen([], () => {
      calls += 1;
      return calls === 1
        ? json(400, {
            title: "Invalid attachment",
            status: 400,
            errors: { file: ["An attachment must be a PDF, JPEG, PNG or HEIC file"] },
          })
        : new Response(
            JSON.stringify({
              title: "Too many attachments",
              code: "attachment_count_exceeded",
              detail: "A customer carries at most 200 attachments.",
              status: 409,
            }),
            { status: 409, headers: { "Content-Type": "application/problem+json" } },
          );
    });
    renderCard(fetchMock);
    await screen.findByText("No attachments yet");
    const input = screen
      .getByText("Add attachment")
      .closest("button")
      ?.parentElement?.querySelector("input[type=file]");
    // user-event honours the input's accept list; the server's refusal is
    // what this test is about, so the file is let through to it.
    await userEvent.upload(
      input as HTMLInputElement,
      new File([new Uint8Array(4)], "notat.txt", { type: "text/plain" }),
      {
        applyAccept: false,
      },
    );
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Attachment could not be added",
          message: "An attachment must be a PDF, JPEG, PNG or HEIC file",
        }),
      ),
    );
    await userEvent.upload(
      input as HTMLInputElement,
      new File([new Uint8Array(4)], "a.pdf", { type: "application/pdf" }),
    );
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Attachment could not be added",
          message: "A customer carries at most 200 attachments.",
        }),
      ),
    );
  });

  it("refuses a file over 10 MB before sending it", async () => {
    const fetchMock = listThen([], () => json(201, attachment()));
    renderCard(fetchMock);
    await screen.findByText("No attachments yet");
    const input = screen
      .getByText("Add attachment")
      .closest("button")
      ?.parentElement?.querySelector("input[type=file]");
    const big = new File([new Uint8Array(1)], "stor.pdf", { type: "application/pdf" });
    Object.defineProperty(big, "size", { value: 10 * 1024 * 1024 + 1 });
    await userEvent.upload(input as HTMLInputElement, big);
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({ message: "The file is larger than 10 MB." }),
      ),
    );
    expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === "POST")).toBe(false);
  });

  it("confirms a delete, calls the endpoint and refreshes", async () => {
    let deleted = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        deleted = true;
        expect(String(input)).toBe("/api/v1/customers/42/attachments/1001");
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(json(200, { data: deleted ? [] : [attachment()] }));
    });
    renderCard(fetchMock);
    await userEvent.click(await screen.findByRole("button", { name: "Delete Rammeavtale.pdf" }));
    expect(await screen.findByText("Delete Rammeavtale.pdf? The file is removed for good.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("No attachments yet")).toBeInTheDocument();
    expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "Attachment removed" }));
  });

  it("downloads the bytes through the session and hands them to the browser", async () => {
    const createObjectURL = vi.fn(() => "blob:avtale");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const fetchMock = listThen([attachment()], (input) => {
      expect(String(input)).toBe("/api/v1/customers/42/attachments/1001");
      return new Response(new Uint8Array([1, 2, 3]), {
        status: 200,
        headers: {
          "Content-Type": "application/pdf",
          "Content-Disposition": "inline; filename*=UTF-8''Rammeavtale.pdf",
        },
      });
    });
    renderCard(fetchMock);
    await userEvent.click(await screen.findByRole("button", { name: "Download Rammeavtale.pdf" }));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
    expect(click).toHaveBeenCalled();
    expect(notifications.show).not.toHaveBeenCalledWith(
      expect.objectContaining({ title: "Attachment could not be downloaded" }),
    );
  });
});
