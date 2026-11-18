/// <reference types="vitest/config" />
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { defineConfig, type Plugin } from "vite";

// Scalar's standalone bundle, copied as it is published (API access design
// K27): it is a classic script that defines window.Scalar, so it is emitted
// beside the page's own script, named by its content, and loaded ahead of it.
// Its MIT notice ships beside it (THIRD-PARTY-NOTICES-<hash>.txt), linked
// from the page as rel="license", so the binary that embeds the bundle
// carries the licence too. Every file under api/reference/ is named by its
// content, which is what lets the server cache them all as immutable.
function scalarStandalone(): Plugin {
  const require = createRequire(import.meta.url);
  const entry = require.resolve("@scalar/api-reference");
  const source = readFileSync(join(dirname(entry), "browser", "standalone.js"));
  const hash = createHash("sha256").update(source).digest("hex").slice(0, 10);
  const fileName = `api/reference/scalar-${hash}.js`;
  const notices = readFileSync(new URL("./THIRD-PARTY-NOTICES.txt", import.meta.url));
  const noticesHash = createHash("sha256").update(notices).digest("hex").slice(0, 10);
  const noticesName = `api/reference/THIRD-PARTY-NOTICES-${noticesHash}.txt`;
  return {
    name: "vantigo-scalar-standalone",
    apply: "build",
    generateBundle() {
      this.emitFile({ type: "asset", fileName, source });
      this.emitFile({ type: "asset", fileName: noticesName, source: notices });
    },
    transformIndexHtml: {
      order: "post",
      handler: () => [
        { tag: "script", attrs: { src: `./${fileName}` }, injectTo: "head" },
        { tag: "link", attrs: { rel: "license", href: `./${noticesName}` }, injectTo: "head" },
      ],
    },
  };
}

// The page is served at /api, and its files at /api/reference/, under
// whatever base path the installation has: every URL in the build is
// relative to the page, so ./api/reference/… resolves beside it.
export default defineConfig({
  base: "./",
  plugins: [scalarStandalone()],
  build: {
    assetsDir: "api/reference",
    sourcemap: false,
    rollupOptions: {
      output: {
        entryFileNames: "api/reference/apiref-[hash].js",
        assetFileNames: "api/reference/[name]-[hash][extname]",
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: false,
  },
});
