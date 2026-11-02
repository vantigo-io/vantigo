// Every frontend workspace as one Vitest run: each project keeps its own
// vite.config.ts (environment, setup files, plugins), and all of them share
// one worker pool, so a small workspace no longer waits for a large one to
// finish before it starts. `bun run frontend:test` runs this; `bun run test`
// inside a workspace still runs that workspace alone.
//
// A plain object rather than defineConfig: Vitest is a dependency of the
// workspaces, not of the repository root, so `vitest/config` does not resolve
// from here. frontend:test runs the host workspace's Vitest against this file.
export default {
  test: {
    projects: ["apps/*/frontend", "packages/*"],
  },
};
