/** At most 10 MB per receipt, and at most ten per expense (contract, design §3.2). */
export const MAX_RECEIPT_BYTES = 10 * 1024 * 1024;
export const MAX_RECEIPTS_PER_ENTRY = 10;

/** What the server sniffs a receipt's bytes down to. Anything else is refused on `file`. */
export const RECEIPT_CONTENT_TYPES = ["image/jpeg", "image/png", "image/heic", "application/pdf"] as const;

/** What a file picker offers — extensions as well as types, because a phone names its own. */
export const RECEIPT_ACCEPT = ".jpg,.jpeg,.jpe,.png,.heic,.heif,.pdf,image/jpeg,image/png,image/heic,application/pdf";

const EXTENSIONS = ["jpg", "jpeg", "jpe", "png", "heic", "heif", "pdf"];

/**
 * Whether the browser can draw this receipt itself. JPEG and PNG go in an
 * `<img>`; HEIC and PDF get a link that opens in a new tab instead. Framing a
 * receipt is impossible on purpose — the platform sends `X-Frame-Options:
 * DENY` and `object-src 'none'`, and the receipt carries its own
 * `default-src 'none'; sandbox` — so there is no `<iframe>`, `<object>` or
 * `<embed>` anywhere in this package.
 */
export const isInlineImage = (contentType: string): boolean =>
  contentType === "image/jpeg" || contentType === "image/png";

/** The refusals this package can see before the upload leaves the browser. */
export type ReceiptRefusal = "tooLarge" | "wrongType" | "tooMany";

/**
 * A pre-check so the person is not made to wait for an upload the server will
 * refuse. The server is still the authority — it sniffs the bytes, and a file
 * named `.pdf` that is really a PNG passes here and is refused there.
 */
export const checkReceiptFile = (file: File, alreadyAttached: number): ReceiptRefusal | undefined => {
  if (alreadyAttached >= MAX_RECEIPTS_PER_ENTRY) return "tooMany";
  if (file.size > MAX_RECEIPT_BYTES) return "tooLarge";
  const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
  const declared = file.type.toLowerCase();
  const knownExtension = EXTENSIONS.includes(extension);
  const knownType = (RECEIPT_CONTENT_TYPES as readonly string[]).includes(declared);
  // The bytes decide, so an unknown extension *and* an unknown type is the
  // only combination worth stopping: a phone that offers
  // `application/octet-stream` under a name with no extension at all.
  return knownExtension || knownType ? undefined : "wrongType";
};

/** A file size written for a human: bytes under a kilobyte, then kB, then MB. */
export const formatFileSize = (bytes: number, formatNumber: (value: number) => string): string => {
  if (bytes < 1024) return `${formatNumber(bytes)} B`;
  if (bytes < 1024 * 1024) return `${formatNumber(Math.round(bytes / 1024))} kB`;
  return `${formatNumber(Math.round((bytes / (1024 * 1024)) * 10) / 10)} MB`;
};
