import { afterEach, describe, expect, it, vi } from "vitest";
import { fittedSize, PHOTO_QUALITY, resizePhoto } from "./photo";

describe("fittedSize", () => {
  it("keeps a small photo as it is", () => {
    expect(fittedSize(1600, 1200)).toEqual({ width: 1600, height: 1200 });
  });

  it("draws the longest edge down to 2560, keeping the proportions", () => {
    expect(fittedSize(4032, 3024)).toEqual({ width: 2560, height: 1920 });
    expect(fittedSize(3024, 4032)).toEqual({ width: 1920, height: 2560 });
  });
});

describe("resizePhoto", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const camera = new File([new Uint8Array(16)], "IMG_0001.HEIC.png", { type: "image/png" });

  it("sends the photo as it was when the browser cannot decode it", async () => {
    vi.stubGlobal("createImageBitmap", undefined);
    expect(await resizePhoto(camera)).toBe(camera);
  });

  it("sends a photo within 2560 px as it was", async () => {
    const close = vi.fn();
    vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue({ width: 2000, height: 1500, close }));
    expect(await resizePhoto(camera)).toBe(camera);
    expect(close).toHaveBeenCalled();
  });

  it("draws a large photo down to 2560 px and sends it as a JPEG at 0.85", async () => {
    const bitmap = { width: 4032, height: 3024, close: vi.fn() };
    vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue(bitmap));
    const drawImage = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
      drawImage,
    } as unknown as CanvasRenderingContext2D);
    const toBlob = vi
      .spyOn(HTMLCanvasElement.prototype, "toBlob")
      .mockImplementation((callback) => callback(new Blob(["jpeg"], { type: "image/jpeg" })));

    const sent = await resizePhoto(camera);

    expect(drawImage).toHaveBeenCalledWith(bitmap, 0, 0, 2560, 1920);
    expect(toBlob).toHaveBeenCalledWith(expect.any(Function), "image/jpeg", PHOTO_QUALITY);
    expect(sent).not.toBe(camera);
    expect(sent.type).toBe("image/jpeg");
    expect(sent.name).toBe("IMG_0001.HEIC.jpg");
    expect(bitmap.close).toHaveBeenCalled();
  });
});
