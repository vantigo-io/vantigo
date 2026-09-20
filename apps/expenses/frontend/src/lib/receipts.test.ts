import { describe, expect, it } from "vitest";
import { checkReceiptFile, isInlineImage, MAX_RECEIPT_BYTES } from "./receipts";

const file = (name: string, type: string, size = 1024): File => {
  const made = new File(["x"], name, { type });
  Object.defineProperty(made, "size", { value: size });
  return made;
};

describe("checkReceiptFile", () => {
  it("passes a photo and a scan", () => {
    expect(checkReceiptFile(file("receipt.jpg", "image/jpeg"), 0)).toBeUndefined();
    expect(checkReceiptFile(file("Faktura nr. 12345", "application/pdf"), 0)).toBeUndefined();
  });

  it("passes a file a phone named without an extension but typed", () => {
    expect(checkReceiptFile(file("image", "image/heic"), 0)).toBeUndefined();
  });

  it("passes a file with a known extension the phone could not type", () => {
    expect(checkReceiptFile(file("scan.heif", "application/octet-stream"), 0)).toBeUndefined();
  });

  it("refuses something that is neither", () => {
    expect(checkReceiptFile(file("notes.txt", "text/plain"), 0)).toBe("wrongType");
  });

  it("refuses more than ten megabytes before the upload leaves the browser", () => {
    expect(checkReceiptFile(file("huge.pdf", "application/pdf", MAX_RECEIPT_BYTES + 1), 0)).toBe("tooLarge");
  });

  it("refuses an eleventh receipt", () => {
    expect(checkReceiptFile(file("receipt.png", "image/png"), 10)).toBe("tooMany");
  });
});

describe("isInlineImage", () => {
  it("is true only for what an img element can draw", () => {
    expect(isInlineImage("image/jpeg")).toBe(true);
    expect(isInlineImage("image/png")).toBe(true);
    expect(isInlineImage("image/heic")).toBe(false);
    expect(isInlineImage("application/pdf")).toBe(false);
  });
});
