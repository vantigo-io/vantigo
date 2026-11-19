import { describe, expect, test } from "bun:test";
import { scanText } from "./check-colours";

const rules = (text: string, allow = []) => scanText("apps/x/frontend/src/a.tsx", text, allow).map((h) => h.rule);

describe("scanText flags", () => {
  test.each([
    ["color: #fff;", "hex colour"],
    ["color: #AABBCC;", "hex colour"],
    ["color: #aabbccdd;", "hex colour"],
    ["background: rgba(0, 0, 0, 0.5);", "rgb/hsl literal"],
    ["background: hsl(10 20% 30%);", "rgb/hsl literal"],
    ['<Box bg="white" />', "light-only Mantine prop"],
    ['<Box bg="gray.0" />', "light-only Mantine prop"],
    ['<Box bg="gray.1" />', "light-only Mantine prop"],
    ['<Text c="dark" />', "light-only Mantine prop"],
    ['<Text c="black" />', "light-only Mantine prop"],
    ["background: var(--mantine-color-gray-1);", "light-only Mantine variable"],
    ["background: var(--mantine-color-white);", "light-only Mantine variable"],
    ["background: var(--mantine-color-vantigo-0);", "vantigo-0 background"],
  ])("%s", (line, rule) => {
    expect(rules(line)).toEqual([rule]);
  });
});

describe("scanText allows", () => {
  test.each([
    "color: light-dark(#fff, #000);",
    "background: light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6));",
    "background: light-dark(\n  var(--mantine-color-white),\n  var(--mantine-color-dark-7)\n);",
    '<Text c="white" />',
    '<Text c="dimmed" />',
    '<Box bg="var(--mantine-color-body)" />',
    "color: var(--mantine-color-gray-6);",
    "const id = '#root-element';",
    "color: #fff; /* colour-lint-ignore: white on a filled swatch */",
  ])("%s", (line) => {
    expect(rules(line)).toEqual([]);
  });

  test("an ignore comment on the line above", () => {
    expect(rules("// colour-lint-ignore: canvas behind a JPEG\nctx.fillStyle = '#fff';")).toEqual([]);
  });

  test("an ignore without a reason does not count", () => {
    expect(rules("color: #fff; // colour-lint-ignore:")).toEqual(["hex colour"]);
  });

  test("the allowlist matches by path and optional line pattern", () => {
    const allow = [{ path: "apps/x/frontend/src/a.tsx", pattern: "fillStyle", reason: "test" }];
    expect(rules("ctx.fillStyle = '#fff';", allow as never)).toEqual([]);
    expect(rules("color: #fff;", allow as never)).toEqual(["hex colour"]);
  });

  test("a light-dark() call does not hide a colour after it", () => {
    expect(rules("a: light-dark(red, blue); b: #fff;")).toEqual(["hex colour"]);
  });
});

test("reports the line number", () => {
  const hits = scanText("a.css", "a {\n  color: #fff;\n}\n");
  expect(hits).toEqual([{ file: "a.css", line: 2, text: "color: #fff;", rule: "hex colour" }]);
});
