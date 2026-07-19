# Runtime-only image: the app is built OUTSIDE docker (natively in CI) and the
# prebuilt, architecture-independent standalone output is copied in. No RUN
# steps touch app code, so `buildx --platform linux/amd64,linux/arm64` needs no
# emulation — it only assembles layers on top of each per-arch base image.
#
# Build context must be the repository root, with `pnpm --filter
# @vantigo/customers build` already executed:
#   docker build -f apps/customers/Dockerfile -t customers .
#
# All configuration is provided at runtime via VANTIGO_CUSTOMERS_* environment
# variables (validated on boot via instrumentation.ts; the container exits
# with code 1 if they are invalid).
#
# Base image: Chainguard hardened Node (distroless-style).
#   - Requires authentication to pull: `chainctl auth configure-docker`
#     (CI logs in via CHAINGUARD_DOCKER_IDENTITY/CHAINGUARD_DOCKER_TOKEN).
#   - No shell or package manager inside; for debugging use `docker debug`
#     or the cgr.dev/refsdal.io/node:latest-dev variant.
#   - Digest bumps arrive as Dependabot PRs and stay within Node 26; a Node
#     major upgrade is a deliberate edit of the :26 tag below (keep in sync
#     with .mise.toml and CI node-version).
#   - Runs as the non-root `node` user (uid 65532) by default.
#
# If a native runtime dependency (e.g. sharp) is ever needed, do NOT move the
# app build into docker; install just that package per-arch in this stage and
# remove it from scripts/prune-standalone.mjs. See repo discussion for the
# full escalation path (Docker Build Cloud / native arm runners).
FROM cgr.dev/refsdal.io/node:26@sha256:313cd39689aa5594d4eb200a296ed30983f46585a6989b298dedd5e38e9c0ada

ENV NODE_ENV=production \
    HOSTNAME=0.0.0.0 \
    PORT=10010

WORKDIR /app

# Standalone output is workspace-shaped: server.js lives in apps/customers/.
COPY --chown=65532:65532 apps/customers/.next/standalone ./
COPY --chown=65532:65532 apps/customers/.next/static ./apps/customers/.next/static
COPY --chown=65532:65532 apps/customers/public ./apps/customers/public

EXPOSE 10010

# Exec-form with node: the image has no shell or wget.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD ["node", "-e", "fetch('http://127.0.0.1:10010/sign-in').then(r=>process.exit(r.ok?0:1),()=>process.exit(1))"]

# The image entrypoint is `node`.
CMD ["apps/customers/server.js"]
