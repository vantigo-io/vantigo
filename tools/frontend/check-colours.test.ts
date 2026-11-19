import { describe, expect, test } from "bun:test";
import { type AllowEntry, compileAllowlist, scanText, staleEntries } from "./check-colours";

const file = "apps/x/frontend/src/a.tsx";
const rules = (text: string, allow: AllowEntry[] = []) =>
  scanText(file, text, compileAllowlist(allow)).map((h) => h.rule);

describe("scanText flags", () => {
  test.each([
    ["color: #fff;", "hex colour"],
    ["color: #AABBCC;", "hex colour"],
    ["color: #aabbccdd;", "hex colour"],
    ["color: #AbCd;", "hex colour"],
    ["background: url(#fff);", "hex colour"],
    ["background: rgba(0, 0, 0, 0.5);", "rgb/hsl/lab literal"],
    ["background: RGB(0 0 0);", "rgb/hsl/lab literal"],
    ["background: hsl(10 20% 30%);", "rgb/hsl/lab literal"],
    ["background: oklch(0.5 0.1 200);", "rgb/hsl/lab literal"],
    ["background: oklab(0.5 0.1 0.1);", "rgb/hsl/lab literal"],
    ["background: lab(50% 10 10);", "rgb/hsl/lab literal"],
    ["background: lch(50% 10 10);", "rgb/hsl/lab literal"],
    ["background: hwb(200 10% 10%);", "rgb/hsl/lab literal"],
    ['<Box bg="white" />', "light-only Mantine prop"],
    ["<Box bg='white' />", "light-only Mantine prop"],
    ['<Box bg="gray.0" />', "light-only Mantine prop"],
    ['<Box bg="gray.1" />', "light-only Mantine prop"],
    ['<Box bg="gray.2" />', "light-only Mantine prop"],
    ['<Box bgc="white" />', "light-only Mantine prop"],
    ['<Box bg={"white"} />', "light-only Mantine prop"],
    ['<Box bg={x ? "dark.7" : "white"} />', "light-only Mantine prop"],
    ["<Box bg={`white`} />", "light-only Mantine prop"],
    ['<Text c="dark" />', "light-only Mantine prop"],
    ['<Text c="black" />', "light-only Mantine prop"],
    ['<Text c={x ? "black" : undefined} />', "light-only Mantine prop"],
    ["<Text c={'dark'} />", "light-only Mantine prop"],
    ['<Badge color="dark" />', "light-only Mantine prop"],
    ['<Badge color={x ? "black" : "red"} />', "light-only Mantine prop"],
    ['style={{ bg: "white" }}', "light-only Mantine prop"],
    ['const s = { color: "white" };', "white/black declaration"],
    ['const s = { color: "black" };', "white/black declaration"],
    ['const s = { background: "white" };', "white/black declaration"],
    ['const s = { backgroundColor: "white" };', "white/black declaration"],
    ['const s = { borderColor: "black" };', "white/black declaration"],
    ['const s = { border: "1px solid white" };', "white/black declaration"],
    ["a { color: white; }", "white/black declaration"],
    ["a { background-color: black; }", "white/black declaration"],
    ["a { border: 1px solid black; }", "white/black declaration"],
    ["a { border-top-color: white; }", "white/black declaration"],
    ["background: var(--mantine-color-gray-0);", "light-only Mantine variable"],
    ["background: var(--mantine-color-gray-2);", "light-only Mantine variable"],
    ["background: var(--mantine-color-white);", "light-only Mantine variable"],
    ["background: var(--mantine-color-vantigo-0);", "vantigo-0 background"],
  ])("%s", (line, rule) => {
    expect(rules(line)).toEqual([rule]);
  });

  test("a light-dark() call does not hide a colour after it", () => {
    expect(rules("a: light-dark(red, blue); b: #fff;")).toEqual(["hex colour"]);
  });

  test("an unclosed light-dark() hides nothing", () => {
    expect(rules("a: light-dark(#fff, #000")).toEqual(["hex colour", "hex colour"]);
  });
});

describe("scanText allows", () => {
  test.each([
    "color: light-dark(#fff, #000);",
    "background: light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6));",
    "background: light-dark(\n  var(--mantine-color-white),\n  var(--mantine-color-dark-7)\n);",
    "background: light-dark(rgba(0, 0, 0, 0.1), rgba(255, 255, 255, 0.1));",
    "color: light-dark(black, white);",
    '<Box bg={x ? "dark.7" : "gray.7"} />',
    '<Text c="white" />',
    '<Text c="dimmed" />',
    '<Box bg="var(--mantine-color-body)" />',
    '<Box bg="gray.3" />',
    "color: var(--mantine-color-gray-6);",
    "background: color-mix(in srgb, var(--mantine-color-blue-6) 20%, transparent);",
    "background: color-mix(in oklab, red, blue);",
    "const label = (x) => x;",
    "const collab = (x) => x;",
    "color: var(--x-white);",
    "--fc-white: 1;",
    "const id = '#root-element';",
    '<a href="#add">Add</a>',
    "<a href='#add'>Add</a>",
    '<a href={"#fade"}>Fade</a>',
    "#fade {\n  color: var(--mantine-color-body);\n}",
    "#fff { margin: 0; }",
    "color: #fff; /* colour-lint-ignore: white on a filled swatch */",
    "color: #fff; // colour-lint-ignore: white on a filled swatch",
  ])("%s", (line) => {
    expect(rules(line)).toEqual([]);
  });

  test("an ignore comment on the line above", () => {
    expect(rules("// colour-lint-ignore: canvas behind a JPEG\nctx.fillStyle = '#fff';")).toEqual([]);
  });

  test("an ignore without a reason does not count", () => {
    expect(rules("color: #fff; // colour-lint-ignore:")).toEqual(["hex colour"]);
  });
});

describe("allowlist", () => {
  test("matches by path and optional line pattern", () => {
    const allow = [{ path: file, pattern: "fillStyle", reason: "test" }];
    expect(rules("ctx.fillStyle = '#fff';", allow)).toEqual([]);
    expect(rules("color: #fff;", allow)).toEqual(["hex colour"]);
  });

  test("an entry without a pattern covers the whole file", () => {
    expect(rules("color: #fff;", [{ path: file, reason: "test" }])).toEqual([]);
  });

  test("a directory prefix covers the files under it", () => {
    const allow = compileAllowlist([{ path: "apps/x/", reason: "test" }]);
    expect(scanText("apps/x/frontend/src/a.css", "color: #fff;", allow)).toEqual([]);
    expect(scanText("apps/y/frontend/src/a.css", "color: #fff;", allow)).toHaveLength(1);
  });

  test("another file's entry does not apply", () => {
    expect(rules("color: #fff;", [{ path: "apps/x/frontend/src/b.tsx", reason: "test" }])).toEqual(["hex colour"]);
  });

  test("an invalid pattern names its entry", () => {
    expect(() => compileAllowlist([{ path: "apps/x/a.ts", pattern: "(", reason: "test" }])).toThrow(
      /entry #1 \(apps\/x\/a\.ts\): invalid pattern/,
    );
  });

  test("an entry needs a reason", () => {
    expect(() => compileAllowlist([{ path: "apps/x/a.ts", reason: " " }])).toThrow(/reason are required/);
  });

  test("an entry that suppressed nothing is stale", () => {
    const allow = compileAllowlist([
      { path: "tools/frontend/check-colours.ts", pattern: "x", reason: "used" },
      { path: "tools/frontend/check-colours.ts", pattern: "never", reason: "unused" },
      { path: "tools/frontend/missing.ts", reason: "gone" },
    ]);
    allow[0].used = 1;
    const root = new URL("../..", import.meta.url).pathname;
    expect(staleEntries(root, allow)).toEqual([
      "tools/frontend/check-colours.ts /never/: suppresses nothing; remove it or fix its pattern",
      "tools/frontend/missing.ts: the path does not exist",
    ]);
  });
});

test("reports the line number", () => {
  const hits = scanText("a.css", "a {\n  color: #fff;\n}\n");
  expect(hits).toEqual([{ file: "a.css", line: 2, text: "color: #fff;", rule: "hex colour" }]);
});
