// Merges every module frontend's tour targets (apps/*/frontend/src/assist-targets.json)
// into the small registry the Assistant's server embeds
// (apps/server/internal/assistant/assist-targets.json): key, route and the English
// description, sorted by key. `go generate` runs it (apps/server/generate.go), and
// the assistant module's test fails when the embedded copy is stale.
import { readdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dir, "../..");

interface PackageTarget {
  key: string;
  route: string;
  description: { en: string; nb: string };
}

export interface ServerTarget {
  key: string;
  route: string;
  description: string;
}

/** The merged registry of the repository at root. Throws on a repeated key. */
export const buildRegistry = async (root: string = repositoryRoot): Promise<ServerTarget[]> => {
  const apps = (await readdir(join(root, "apps"), { withFileTypes: true })).filter((entry) => entry.isDirectory());
  const merged = new Map<string, ServerTarget>();
  for (const app of apps.map((entry) => entry.name).sort()) {
    const file = join(root, "apps", app, "frontend", "src", "assist-targets.json");
    let text: string;
    try {
      text = await readFile(file, "utf8");
    } catch {
      continue;
    }
    for (const target of JSON.parse(text) as PackageTarget[]) {
      if (merged.has(target.key)) throw new Error(`${target.key} is registered twice (second time in ${app})`);
      merged.set(target.key, { key: target.key, route: target.route, description: target.description.en });
    }
  }
  return [...merged.values()].sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
};

export const output = "apps/server/internal/assistant/assist-targets.json";

if (import.meta.main) {
  const registry = await buildRegistry();
  await writeFile(join(repositoryRoot, output), `${JSON.stringify(registry, null, 2)}\n`);
  console.log(`${registry.length} tour target(s) written to ${output}`);
}
