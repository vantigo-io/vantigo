import { type AssistTarget, assistTargetProblems } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import targets from "./assist-targets.json";

// The package's own source, tests and generated files aside: the markers a tour can point at.
const sources = import.meta.glob(["./**/*.{ts,tsx}", "!./**/*.test.{ts,tsx}", "!./test/**", "!./api-schema.d.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

describe("the Assistant's tour targets", () => {
  it("are all marked in the source, and every marker is registered", () => {
    expect(assistTargetProblems(targets as AssistTarget[], sources)).toEqual([]);
  });

  it("only name this module's own controls", () => {
    for (const target of targets) expect(target.key.startsWith("accounting.")).toBe(true);
  });
});
