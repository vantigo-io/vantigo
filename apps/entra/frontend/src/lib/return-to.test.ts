import { describe, expect, it } from "vitest";
import { CONNECTIONS_PATH, safeReturnTo } from "./return-to";

describe("safeReturnTo", () => {
  const cases: [string, string | undefined, string][] = [
    ["a path of this installation", "/calendar/settings", "/calendar/settings"],
    ["a path with a query", "/calendar/settings?tab=outlook", "/calendar/settings?tab=outlook"],
    ["an encoded backslash, which is a path", "/%5Cx", "/%5Cx"],
    ["nothing", undefined, CONNECTIONS_PATH],
    ["an empty value", "", CONNECTIONS_PATH],
    ["a protocol-relative address", "//x", CONNECTIONS_PATH],
    ["a slash and a backslash", "/\\x", CONNECTIONS_PATH],
    ["a backslash further on", "/a\\b", CONNECTIONS_PATH],
    ["another origin", "https://x", CONNECTIONS_PATH],
    ["a script", "javascript:x", CONNECTIONS_PATH],
    ["a relative path", "calendar/settings", CONNECTIONS_PATH],
    ["a line break", "/a\nb", CONNECTIONS_PATH],
    ["a tab", "/a\tb", CONNECTIONS_PATH],
    ["a delete character", "/a\x7f", CONNECTIONS_PATH],
    ["513 bytes", `/${"a".repeat(512)}`, CONNECTIONS_PATH],
  ];

  for (const [name, value, expected] of cases)
    it(`answers ${expected} for ${name}`, () => expect(safeReturnTo(value)).toBe(expected));

  it("keeps a path of exactly 512 bytes", () => {
    const path = `/${"a".repeat(511)}`;
    expect(safeReturnTo(path)).toBe(path);
  });

  it("counts bytes, not characters", () => {
    expect(safeReturnTo(`/${"ø".repeat(256)}`)).toBe(CONNECTIONS_PATH);
    expect(safeReturnTo(`/${"ø".repeat(255)}`)).toBe(`/${"ø".repeat(255)}`);
  });
});
