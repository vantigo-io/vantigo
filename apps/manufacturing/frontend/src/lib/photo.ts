/** The longest edge a check's photo is sent at, in pixels. */
export const PHOTO_MAX_EDGE = 2560;

/** The JPEG quality a resized photo is encoded at. */
export const PHOTO_QUALITY = 0.85;

/** The size a photo of width × height is drawn at: its longest edge at most `maxEdge`, its proportions kept. */
export const fittedSize = (width: number, height: number, maxEdge = PHOTO_MAX_EDGE) => {
  const scale = Math.min(1, maxEdge / Math.max(width, height));
  return { width: Math.round(width * scale), height: Math.round(height * scale) };
};

/**
 * A check's photo made small before it is uploaded (manufacturing phase 2
 * M97): a tablet's camera takes pictures of several megabytes, and the
 * server keeps one of at most 10 MB. A picture whose longest edge is past
 * 2560 px is drawn down to it and sent as a JPEG at 0.85. A picture already
 * small enough, or one the browser cannot decode or draw, is sent as it was —
 * the server still judges it.
 */
export const resizePhoto = async (file: File): Promise<File> => {
  if (typeof createImageBitmap !== "function") return file;
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return file;
  }
  try {
    if (Math.max(bitmap.width, bitmap.height) <= PHOTO_MAX_EDGE) return file;
    const size = fittedSize(bitmap.width, bitmap.height);
    const canvas = document.createElement("canvas");
    canvas.width = size.width;
    canvas.height = size.height;
    const context = canvas.getContext("2d");
    if (!context) return file;
    context.drawImage(bitmap, 0, 0, size.width, size.height);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", PHOTO_QUALITY));
    if (!blob) return file;
    const name = file.name.replace(/\.[^.]*$/, "") || "photo";
    return new File([blob], `${name}.jpg`, { type: "image/jpeg" });
  } finally {
    bitmap.close();
  }
};
