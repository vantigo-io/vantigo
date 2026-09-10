import { afterEach, describe, expect, it } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkToolchain } from "./check";

const roots: string[] = [];

const repository = async (files: Record<string, string>): Promise<string> => {
  const root = await mkdtemp(join(tmpdir(), "toolchain-"));
  roots.push(root);
  for (const [path, content] of Object.entries(files)) {
    await mkdir(join(root, path, ".."), { recursive: true });
    await writeFile(join(root, path), content);
  }
  return root;
};

const consistent = {
  "mise.toml":
    '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\ngo = "1.27.0"\n"go:golang.org/x/vuln/cmd/govulncheck" = "1.7.0"\n',
  "global.json": JSON.stringify({ sdk: { version: "10.0.302" } }),
  ".bun-version": "1.3.14\n",
  "apps/server/go.mod": "module github.com/vantigo-io/vantigo/server\n\ngo 1.27.0\n",
};

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("toolchain check", () => {
  it("passes when every pin agrees", async () => {
    expect(await checkToolchain(await repository(consistent))).toEqual([]);
  });

  it("reports a go.mod go directive that differs from the mise go pin", async () => {
    const root = await repository({
      ...consistent,
      "apps/server/go.mod": "module github.com/vantigo-io/vantigo/server\n\ngo 1.28.0\n",
    });

    expect(await checkToolchain(root)).toEqual([
      { tool: "go", message: "mise.toml pins 1.27.0 but apps/server/go.mod declares go 1.28.0." },
    ]);
  });

  it("reports a missing go pin", async () => {
    const root = await repository({
      ...consistent,
      "mise.toml": '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\n',
    });

    expect(await checkToolchain(root)).toEqual([{ tool: "go", message: "mise.toml does not pin a go version." }]);
  });

  it("does not mistake go-installed tools for the go pin", async () => {
    const root = await repository({
      ...consistent,
      "mise.toml": '[tools]\ndotnet = "10.0.302"\nbun = "1.3.14"\n"go:golang.org/x/vuln/cmd/govulncheck" = "1.7.0"\n',
    });

    expect(await checkToolchain(root)).toEqual([{ tool: "go", message: "mise.toml does not pin a go version." }]);
  });
});
