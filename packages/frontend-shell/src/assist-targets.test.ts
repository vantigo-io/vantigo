import { describe, expect, it } from "vitest";
import {
  type AssistTarget,
  assistSelector,
  assistTargetProblems,
  routeHasParams,
  routeMatches,
} from "./assist-targets";

const target = (key: string, route = "/things"): AssistTarget => ({
  key,
  route,
  label: { en: "Thing", nb: "Ting" },
  description: { en: "A thing.", nb: "En ting." },
});

describe("assistTargetProblems", () => {
  it("accepts a registry whose keys and markers agree", () => {
    const sources = { "./a.tsx": '<Button data-assist="m.things.new" />\n<Input data-assist="m.things.name" />' };
    expect(assistTargetProblems([target("m.things.new"), target("m.things.name")], sources)).toEqual([]);
  });

  it("reports a registered key with no marker", () => {
    expect(assistTargetProblems([target("m.things.new")], { "./a.tsx": "<Button />" })).toEqual([
      'm.things.new: no data-assist="m.things.new" in the source',
    ]);
  });

  it("reports a marker that is not registered", () => {
    expect(assistTargetProblems([], { "./a.tsx": '<Button data-assist="m.things.old" />' })).toEqual([
      './a.tsx: data-assist="m.things.old" is not registered',
    ]);
  });

  it("refuses a marker that is not a string literal, so the check cannot be sidestepped", () => {
    const sources = { "./a.tsx": "<Button data-assist={key} />" };
    expect(assistTargetProblems([], sources)).toEqual(["./a.tsx: data-assist must be a string literal"]);
  });

  it("reports a malformed or repeated key, a route that is not a path and an empty text", () => {
    const bad = { ...target("Things", "things"), label: { en: "", nb: "x" } };
    const problems = assistTargetProblems([bad, target("m.things.new"), target("m.things.new")], {
      "./a.tsx": 'data-assist="m.things.new"',
    });
    expect(problems).toContain("Things: not <module>.<screen>.<control>");
    expect(problems).toContain("Things: route must be a path");
    expect(problems).toContain("Things: label.en is empty");
    expect(problems).toContain("m.things.new: registered twice");
  });
});

describe("routes", () => {
  it("finds record routes and matches them against a path", () => {
    expect(routeHasParams("/customers/{customerId}")).toBe(true);
    expect(routeHasParams("/customers")).toBe(false);
    expect(routeMatches("/customers/{customerId}", "/customers/42")).toBe(true);
    expect(routeMatches("/customers", "/customers/42")).toBe(false);
    expect(routeMatches("/energy/metering-points", "/energy/metering-points")).toBe(true);
  });

  it("finds a parameter only between a brace and the next one, in linear time", () => {
    const cases: [string, boolean][] = [
      ["/x/{id}", true],
      ["/x/{}", false],
      ["/x/{}/{id}", true],
      ["/x/{{}", true],
      ["/x/{", false],
      ["/x/}{", false],
      ["/x/{a", false],
      ["", false],
    ];
    for (const [route, want] of cases) {
      expect(routeHasParams(route)).toBe(want);
    }
    // The input the regular expression was slow on answers at once.
    const started = performance.now();
    expect(routeHasParams("{".repeat(100_000))).toBe(false);
    expect(performance.now() - started).toBeLessThan(100);
  });

  it("builds a selector only from a well-formed key", () => {
    expect(assistSelector("m.things.new")).toBe('[data-assist="m.things.new"]');
    expect(assistSelector('x"] , body [a="')).toBe('[data-assist=""]');
  });
});
