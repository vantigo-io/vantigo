import { describe, expect, it } from "vitest";
import { isAllowedHref } from "./extensions";
import { exportSchema } from "./schema";
import recorded from "./schema.json";

/**
 * The checked-in export of the editor's schema (knowledge design K8), which
 * the server's Go test compares with its allowlist. After changing the
 * editor's extensions, run the package's `schema:export` script and change
 * the allowlist with it.
 */
describe("the editor's schema", () => {
  it("matches schema.json", () => {
    expect(
      exportSchema(),
      "the editor's schema drifted: run `bun run schema:export` and update the server's allowlist",
    ).toEqual(recorded);
  });

  it("allows only the link targets the server does", () => {
    for (const href of ["https://example.com", "http://a.no", "mailto:kari@example.com", "/knowledge/articles/1"])
      expect(isAllowedHref(href), href).toBe(true);
    for (const href of ["javascript:alert(1)", "//evil.example", "data:text/html,x", "ftp://a"])
      expect(isAllowedHref(href), href).toBe(false);
  });
});
