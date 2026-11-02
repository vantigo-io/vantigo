import { expect, test } from "bun:test";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildRegistry } from "./gen-targets";

const write = async (root: string, app: string, targets: unknown) => {
  const dir = join(root, "apps", app, "frontend", "src");
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "assist-targets.json"), JSON.stringify(targets));
};

const entry = (key: string) => ({
  key,
  route: "/x",
  label: { en: "L", nb: "L" },
  description: { en: `en ${key}`, nb: `nb ${key}` },
});

test("merges the packages' targets, sorted by key, with the English description", async () => {
  const root = await mkdtemp(join(tmpdir(), "assist-"));
  await write(root, "b", [entry("b.s.two"), entry("b.s.one")]);
  await write(root, "a", [entry("a.s.one")]);
  await mkdir(join(root, "apps", "no-targets", "frontend", "src"), { recursive: true });
  expect(await buildRegistry(root)).toEqual([
    { key: "a.s.one", route: "/x", description: "en a.s.one" },
    { key: "b.s.one", route: "/x", description: "en b.s.one" },
    { key: "b.s.two", route: "/x", description: "en b.s.two" },
  ]);
});

test("refuses a key registered by two packages", async () => {
  const root = await mkdtemp(join(tmpdir(), "assist-"));
  await write(root, "a", [entry("m.s.one")]);
  await write(root, "b", [entry("m.s.one")]);
  expect(buildRegistry(root)).rejects.toThrow("registered twice");
});
