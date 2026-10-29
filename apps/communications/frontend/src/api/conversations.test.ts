import { afterEach, describe, expect, it, vi } from "vitest";
import { type Attachment, fetchInlineImage, inlineImageMaxBytes, inlineImages } from "./conversations";

const image = (overrides: Partial<Attachment> = {}): Attachment => ({
  id: "img-1",
  fileName: "logo.png",
  contentType: "image/png",
  sizeBytes: 3,
  contentId: "logo@x",
  scanStatus: "unscanned",
  isInline: true,
  createdAt: "2026-01-01T00:00:00Z",
  downloadAvailable: true,
  downloadPath: "/api/v1/communications/attachments/img-1/download",
  ...overrides,
});

afterEach(() => vi.unstubAllGlobals());

describe("inline images", () => {
  it("fetches an inline image with the session and answers it as a data: URI", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(new Uint8Array([1, 2, 3]), { headers: { "Content-Type": "image/png" } })),
    );
    await expect(fetchInlineImage("/api/v1/communications/attachments/img-1/download")).resolves.toBe(
      "data:image/png;base64,AQID",
    );
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/communications/attachments/img-1/download",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("refuses a type other than PNG, GIF, JPEG or WebP, a failed answer and an image too large", async () => {
    const answer = (body: BodyInit, type: string, status = 200) =>
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(new Response(body, { status, headers: { "Content-Type": type } })),
      );
    answer("<svg/>", "image/svg+xml");
    await expect(fetchInlineImage("/x")).resolves.toBeNull();
    answer("<p>", "text/html");
    await expect(fetchInlineImage("/x")).resolves.toBeNull();
    answer("", "image/png", 404);
    await expect(fetchInlineImage("/x")).resolves.toBeNull();
    answer(new Uint8Array(inlineImageMaxBytes + 1), "image/png");
    await expect(fetchInlineImage("/x")).resolves.toBeNull();
  });

  it("substitutes only the message's own images the HTML names, of an allowed type and size", async () => {
    const fetchImage = vi.fn().mockResolvedValue("data:image/png;base64,AQID");
    const html =
      '<img src="/api/v1/communications/attachments/img-1/download"><img src="/api/v1/communications/attachments/img-9/download">';
    const result = await inlineImages(
      html,
      [
        image(),
        image({
          id: "svg",
          contentType: "image/svg+xml",
          downloadPath: "/api/v1/communications/attachments/img-9/download",
        }),
        image({ id: "big", sizeBytes: inlineImageMaxBytes + 1, downloadPath: "/big" }),
        image({ id: "unnamed", downloadPath: "/api/v1/communications/attachments/img-2/download" }),
      ],
      fetchImage,
    );
    expect(result).toBe(
      '<img src="data:image/png;base64,AQID"><img src="/api/v1/communications/attachments/img-9/download">',
    );
    expect(fetchImage).toHaveBeenCalledTimes(1);
  });

  it("leaves an image the fetch could not turn into data: as it was", async () => {
    const html = '<img src="/api/v1/communications/attachments/img-1/download">';
    await expect(inlineImages(html, [image()], vi.fn().mockRejectedValue(new Error("offline")))).resolves.toBe(html);
  });
});
