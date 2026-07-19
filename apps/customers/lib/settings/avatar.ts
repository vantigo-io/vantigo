const AVATAR_SIZE = 256;
const AVATAR_QUALITY = 0.85;
export const AVATAR_MAX_FILE_BYTES = 8 * 1024 * 1024;

/**
 * Resizes an image file to a small square webp data URL suitable for
 * storing directly in the users.image column (no object storage yet).
 */
export async function fileToAvatarDataUrl(file: File): Promise<string> {
  const bitmap = await createImageBitmap(file);

  try {
    const side = Math.min(bitmap.width, bitmap.height);
    const sx = (bitmap.width - side) / 2;
    const sy = (bitmap.height - side) / 2;
    const size = Math.min(AVATAR_SIZE, side);

    const canvas = document.createElement("canvas");
    canvas.width = size;
    canvas.height = size;

    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Canvas 2d context unavailable");

    ctx.drawImage(bitmap, sx, sy, side, side, 0, 0, size, size);
    return canvas.toDataURL("image/webp", AVATAR_QUALITY);
  } finally {
    bitmap.close();
  }
}
