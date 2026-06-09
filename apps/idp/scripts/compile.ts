import { $ } from "bun";

let target = process.argv[2];
if (!target) {
  // Auto-detect host platform
  const os = process.platform === "win32" ? "windows" : process.platform;
  const arch = process.arch;
  target = `${os}-${arch}`;
  console.log(`No target specified. Auto-detected host platform: ${target}`);
}

// Map the target to a standardized docker/distribution suffix
let suffix = target;
if (target === "linux-x64") {
  suffix = "linux-amd64";
} else if (target === "linux-arm64") {
  suffix = "linux-arm64";
} else if (target === "darwin-arm64") {
  suffix = "darwin-arm64";
} else if (target === "windows-x64") {
  suffix = "windows-amd64.exe";
} else if (target === "darwin-x64") {
  suffix = "darwin-amd64";
}

const outfile = `../../dist/vantigo-idp-${suffix}`;

console.log(`Compiling apps/idp for target: ${target} -> ${outfile}`);

// Ensure the frontend web assets are built first
await $`bun run build:web`;

// Compile the standalone binary for the specified target
await $`bun build ./src/entry.docker.ts --compile --target=bun-${target} --outfile=${outfile}`;

console.log(`Successfully compiled IDP standalone binary to: ${outfile}`);
