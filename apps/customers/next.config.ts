import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./lib/i18n/request.ts");

const nextConfig: NextConfig = {
  output: "standalone",
  // The container image is built once and runs on both amd64 and arm64, so the
  // standalone output must stay free of native binaries. sharp (used only by
  // next/image optimization) is platform-specific — the optimizer is disabled
  // and sharp is pruned from the standalone output after build (see
  // scripts/prune-standalone.mjs; Next force-includes sharp regardless of
  // outputFileTracingExcludes). If image optimization is ever needed, install
  // sharp per-arch in the runtime image instead.
  images: {
    unoptimized: true,
  },
  reactCompiler: true,
  experimental: {
    optimizePackageImports: ["@mantine/core", "@mantine/hooks", "@tabler/icons-react"],
  },
};

export default withNextIntl(nextConfig);
