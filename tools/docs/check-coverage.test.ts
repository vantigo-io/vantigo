import { describe, expect, test } from "bun:test";
import { check, type Page, readSources } from "./check-coverage";

const page = (path: string, ...sources: string[]): Page => ({ path: `docs/src/content/docs/en/${path}`, sources });
const en = (p: string) => `docs/src/content/docs/en/${p}`;
const nb = (p: string) => `docs/src/content/docs/nb/${p}`;

describe("readSources", () => {
  test("reads a block list", () => {
    expect(readSources("---\ntitle: x\nsources:\n  - apps/a\n  - apps/b\n---\nbody")).toEqual(["apps/a", "apps/b"]);
  });
  test("reads an inline list", () => {
    expect(readSources('---\nsources: ["apps/a", apps/b]\n---')).toEqual(["apps/a", "apps/b"]);
  });
  test("stops at the next key", () => {
    expect(readSources("---\nsources:\n  - apps/a\nsidebar:\n  order: 1\n---")).toEqual(["apps/a"]);
  });
  test("is empty without frontmatter or key", () => {
    expect(readSources("# no frontmatter")).toEqual([]);
    expect(readSources("---\ntitle: x\n---")).toEqual([]);
  });
});

describe("check", () => {
  const pages = [
    page("reference/invoices.md", "apps/server/internal/invoices", "openapi/invoices.yaml"),
    page("admin/authentication.md", "apps/server/internal/identity"),
  ];

  test("a covered change without its page is undocumented", () => {
    const findings = check(["apps/server/internal/invoices/payments.go"], pages);
    expect(findings).toEqual([
      { kind: "undocumented", file: "apps/server/internal/invoices/payments.go", pages: [en("reference/invoices.md")] },
    ]);
  });

  test("a covered change with its page passes", () => {
    expect(check(["apps/server/internal/invoices/payments.go", en("reference/invoices.md")], pages)).toEqual([]);
  });

  test("the page in the other language also counts", () => {
    const findings = check(
      ["apps/server/internal/identity/login.go", nb("admin/authentication.md"), en("admin/authentication.md")],
      pages,
    );
    expect(findings).toEqual([]);
  });

  test("a change nobody covers is only a notice", () => {
    expect(check(["apps/server/internal/newmodule/x.go"], pages)).toEqual([
      { kind: "uncovered", file: "apps/server/internal/newmodule/x.go" },
    ]);
  });

  test("tests, docs and tooling never need documentation", () => {
    expect(
      check(
        ["apps/server/internal/invoices/payments_test.go", "tools/docs/x.ts", ".github/workflows/ci.yml", "bun.lock"],
        pages,
      ),
    ).toEqual([]);
  });

  test("a one-sided edit of a bilingual page is untranslated", () => {
    expect(check([en("admin/authentication.md")], pages)).toEqual([
      { kind: "untranslated", file: en("admin/authentication.md"), detail: nb("admin/authentication.md") },
    ]);
    expect(check([en("admin/authentication.md"), nb("admin/authentication.md")], pages)).toEqual([]);
  });

  test("reference pages are English only", () => {
    expect(check([en("reference/invoices.md")], pages)).toEqual([]);
  });
});
