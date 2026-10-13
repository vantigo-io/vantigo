/**
 * A QR code the server rendered, as an image's address (invoices payments and
 * reminders design D22, plan reading 10): the SVG markup base64-encoded into a
 * `data:` URL, for an `<img src>`. The markup is never put into the page as
 * elements, and the app never draws a QR code of its own.
 */
export const svgDataUrl = (svg: string): string => {
  const bytes = new TextEncoder().encode(svg);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `data:image/svg+xml;base64,${btoa(binary)}`;
};
