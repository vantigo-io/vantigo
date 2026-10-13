import { describe, expect, it } from "vitest";
import { svgDataUrl } from "./qr";

describe("svgDataUrl", () => {
  it("encodes the server's SVG as UTF-8 base64, so a non-ASCII title survives", () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg"><title>Betal fakturaen – 1 250,00 kr æøå</title></svg>';
    const url = svgDataUrl(svg);

    expect(url.startsWith("data:image/svg+xml;base64,")).toBe(true);
    const binary = atob(url.slice("data:image/svg+xml;base64,".length));
    const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
    expect(new TextDecoder().decode(bytes)).toBe(svg);
  });
});
