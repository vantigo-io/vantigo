import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const rootDir = resolve(import.meta.dir, "../..");

export type ToolchainIssue = {
  tool: string;
  message: string;
};

const readMiseVersion = (miseToml: string, tool: string): string | null => {
  const match = miseToml.match(new RegExp(`^\\s*${tool}\\s*=\\s*"([^"]+)"`, "m"));
  return match?.[1] ?? null;
};

export const checkToolchain = async (repositoryRoot: string): Promise<ToolchainIssue[]> => {
  const issues: ToolchainIssue[] = [];

  const miseToml = await readFile(resolve(repositoryRoot, "mise.toml"), "utf8");
  const globalJson = JSON.parse(await readFile(resolve(repositoryRoot, "global.json"), "utf8"));
  const bunVersion = (await readFile(resolve(repositoryRoot, ".bun-version"), "utf8")).trim();

  const miseDotnet = readMiseVersion(miseToml, "dotnet");
  const miseBun = readMiseVersion(miseToml, "bun");
  const sdkVersion = globalJson?.sdk?.version ?? null;

  if (miseDotnet === null) {
    issues.push({ tool: "dotnet", message: "mise.toml does not pin a dotnet version." });
  } else if (miseDotnet !== sdkVersion) {
    issues.push({
      tool: "dotnet",
      message: `mise.toml pins ${miseDotnet} but global.json sdk.version is ${sdkVersion}.`,
    });
  }

  if (miseBun === null) {
    issues.push({ tool: "bun", message: "mise.toml does not pin a bun version." });
  } else if (miseBun !== bunVersion) {
    issues.push({
      tool: "bun",
      message: `mise.toml pins ${miseBun} but .bun-version is ${bunVersion}.`,
    });
  }

  return issues;
};

export const runCheck = async (repositoryRoot: string): Promise<number> => {
  const issues = await checkToolchain(repositoryRoot);

  if (issues.length > 0) {
    console.error("Toolchain versions have drifted. mise.toml is the source of truth:");
    for (const issue of issues) console.error(`- ${issue.tool}: ${issue.message}`);
    return 1;
  }

  console.log("Toolchain checks passed (mise.toml agrees with global.json and .bun-version).");
  return 0;
};

if (import.meta.main) {
  try {
    process.exit(await runCheck(rootDir));
  } catch (error) {
    console.error("Toolchain check could not inspect the repository:");
    console.error(error instanceof Error ? error.message : error);
    process.exit(1);
  }
}
