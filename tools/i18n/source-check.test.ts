import { describe, expect, it } from "bun:test";
import { findUntranslatedLiterals, shouldScanProductionSourceFile } from "./source-check";

describe("frontend production i18n source check", () => {
  it("finds static JSX text and user-visible prop strings", () => {
    const issues = findUntranslatedLiterals(
      `
        export const Example = () => (
          <section title="Account">
            <button aria-label="Save changes">Save</button>
            <input placeholder="Search" />
            <p>{"Details"}</p>
          </section>
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toHaveLength(5);
    expect(issues.map((issue) => issue.kind)).toEqual(["jsx-prop", "jsx-prop", "jsx-text", "jsx-prop", "jsx-text"]);
  });

  it("ignores technical values, URLs, dynamic data, and infrastructure files", () => {
    const issues = findUntranslatedLiterals(
      `
        export const Example = ({ record }: { record: { title: string; name: string } }) => (
          <div className="card" style={{ color: "var(--color)" }}>
            <a href="https://example.test">{record.name}</a>
            <input type="email" title={record.title} label="GSRN" />
            <span>·</span>
          </div>
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toEqual([]);
    expect(shouldScanProductionSourceFile("apps/example/frontend/src/example.test.tsx")).toBe(false);
    expect(shouldScanProductionSourceFile("apps/example/frontend/src/api/client.ts")).toBe(false);
    expect(shouldScanProductionSourceFile("apps/example/frontend/src/catalog.ts")).toBe(false);
  });

  it("finds statically resolvable copy nested in props, arrays, branches, and templates", () => {
    const issues = findUntranslatedLiterals(
      `
        const showAlt = true;
        const options = [{ label: "Choose an account" }, { title: showAlt ? "Alternative" : "Fallback" }];
        export const Example = () => (
          <Widget
            config={{ description: "Pick an account", options }}
            label={showAlt ? "Continue" : "Proceed"}
            title={\`Hello \${"there"}\`}
          />
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toHaveLength(5);
    expect(issues.map((issue) => issue.kind)).toEqual(["jsx-prop", "jsx-prop", "jsx-prop", "jsx-prop", "jsx-prop"]);
  });

  it("reports natural-language text in JSX prop fragments while preserving technical values", () => {
    const issues = findUntranslatedLiterals(
      `
        export const Example = () => (
          <Widget
            title={<>Products</>}
            code={<Code>TypeScript</Code>}
            label={<Badge>SKU</Badge>}
          />
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toHaveLength(1);
    expect(issues[0]?.kind).toBe("jsx-text");
    expect(issues[0]?.message).toContain("static user-visible text");
  });

  it("reports direct natural-language headings in Title and Text components", () => {
    const issues = findUntranslatedLiterals(
      `
        export const Example = () => (
          <>
            <Title>Products</Title>
            <Text>Products</Text>
            <Text>PRODUCTS</Text>
          </>
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toHaveLength(3);
    expect(issues.map((issue) => issue.kind)).toEqual(["jsx-text", "jsx-text", "jsx-text"]);
  });

  it("does not report technical nested values or dynamic/API data", () => {
    const issues = findUntranslatedLiterals(
      `
        const technical = [{ label: "GSRN" }, { title: "/api/v1/items" }, { description: "--color-text" }];
        export const Example = ({ record }: { record: { label: string } }) => (
          <Widget
            options={technical}
            config={{ label: record.label, title: "https://example.test" }}
            value={record.label}
          />
        );
      `,
      "apps/example/frontend/src/example.tsx",
    );

    expect(issues).toEqual([]);
  });

  it("reports an intentional violation so the quality-gate command can fail", () => {
    const issues = findUntranslatedLiterals(
      `export const IntentionalViolation = () => <button>Translate me</button>;`,
      "apps/example/frontend/src/intentional-violation.tsx",
    );

    expect(issues).toHaveLength(1);
    expect(issues[0]?.message).toContain("use t(...)");
  });
});
