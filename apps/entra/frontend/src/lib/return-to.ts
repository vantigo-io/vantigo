/** Where a connect returns when it named nowhere, or nowhere safe: the Connections page. */
export const CONNECTIONS_PATH = "/settings/connections";

const MAX_BYTES = 512;
const origin = "http://h";

/**
 * A `returnTo` held to a path on this installation (Outlook connection plan
 * reading 15, the server's `safeReturnTo` rule): at most 512 bytes, starting
 * with one `/` and never `//` or `/\`, no backslash anywhere, no control
 * character, and resolving against an origin to that same origin. Anything
 * else, and an empty value, becomes the Connections page. Checked again here
 * before navigating, though the server checked it when it sealed it.
 */
export const safeReturnTo = (value: string | undefined | null): string => {
  if (!value || new TextEncoder().encode(value).length > MAX_BYTES) return CONNECTIONS_PATH;
  if (!value.startsWith("/") || value[1] === "/" || value[1] === "\\" || value.includes("\\")) return CONNECTIONS_PATH;
  for (const char of value) {
    const code = char.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) return CONNECTIONS_PATH;
  }
  try {
    return new URL(value, origin).origin === origin ? value : CONNECTIONS_PATH;
  } catch {
    return CONNECTIONS_PATH;
  }
};
