/**
 * The links the support line renders. The server validates the contacts
 * before it stores them, and the client checks again before it puts one in an
 * href: a value that arrives in any other way (a stale mirror, a bug) must not
 * become a script link or an extra mailto header.
 */

// biome-ignore lint/suspicious/noControlCharactersInRegex: control characters are what is refused
const CONTROL = /[\u0000-\u001f\u007f]/;

/** The link, when it is an absolute http(s) URL or a same-origin path; otherwise null. */
export function safeSupportUrl(value: string): string | null {
  if (CONTROL.test(value)) return null;
  if (value.startsWith("/")) {
    return value.startsWith("//") || value.startsWith("/\\") ? null : value;
  }
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && url.host !== "" ? value : null;
  } catch {
    return null;
  }
}

/** A mailto: link for a plain address; the address is encoded, so it cannot add a header or a recipient. */
export function supportMailto(email: string): string | null {
  if (CONTROL.test(email) || /[?&%]/.test(email)) return null;
  return `mailto:${encodeURIComponent(email).replace(/%40/g, "@")}`;
}
