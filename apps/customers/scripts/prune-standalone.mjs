#!/usr/bin/env node
/**
 * Prunes platform-specific native packages from the Next.js standalone output
 * so a single build produces an architecture-independent artifact that runs on
 * both linux/amd64 and linux/arm64.
 *
 * sharp is only used by next/image optimization, which is disabled
 * (images.unoptimized in next.config.ts), yet Next always traces it into the
 * standalone output. Any other native binary left after pruning is an error:
 * it would silently break the "build once, run on any arch" container.
 */
import { globSync, rmSync } from "node:fs";
import path from "node:path";

const standaloneDir = path.join(import.meta.dirname, "..", ".next", "standalone");

const pruneGlobs = [
  "node_modules/.pnpm/sharp@*",
  "node_modules/.pnpm/@img+*",
  "node_modules/sharp",
  "node_modules/@img",
];

let pruned = 0;
for (const pattern of pruneGlobs) {
  for (const match of globSync(pattern, { cwd: standaloneDir })) {
    rmSync(path.join(standaloneDir, match), { recursive: true, force: true });
    pruned += 1;
  }
}
console.log(`[prune-standalone] Removed ${pruned} sharp/@img entries.`);

const nativeBinaries = globSync("**/*.node", { cwd: standaloneDir });
if (nativeBinaries.length > 0) {
  console.error(
    "[prune-standalone] Native binaries found in standalone output — these are " +
      "platform-specific and break the multi-arch container image:\n" +
      nativeBinaries.map((file) => `  - ${file}`).join("\n"),
  );
  process.exit(1);
}
console.log("[prune-standalone] Standalone output is free of native binaries.");
