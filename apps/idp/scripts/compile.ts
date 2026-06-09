import { $ } from "bun";

const target =
  process.argv[2] ||
  `${process.platform === "win32" ? "windows" : process.platform}-${process.arch}`;

// Map to standardized docker/distribution suffix
const suffixes: Record<string, string> = {
  "linux-x64": "linux-amd64",
  "linux-arm64": "linux-arm64",
  "darwin-arm64": "darwin-arm64",
  "darwin-x64": "darwin-amd64",
  "windows-x64": "windows-amd64.exe",
};

const suffix = suffixes[target] || target;
const outfile = `../../dist/vantigo-idp-${suffix}`;

console.log(
  `Compiling apps/idp standalone binary: target=${target} -> outfile=${outfile}`,
);

await $`bun build ./src/entry.docker.ts --compile --target=bun-${target} --outfile=${outfile}`;
