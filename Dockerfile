# syntax=docker/dockerfile:1

# Vantigo — one image, one static Go binary; the command is argv[1]
# (apps/server/cmd/vantigo is the dispatch table): api (the default here),
# server, worker, migrate, seed, healthcheck.
#
# NOTHING COMPILES IN HERE. Build the binaries natively first:
#
#   bash scripts/build-artifacts.sh   # → dist/server/linux/{amd64,arm64}/vantigo
#
# This file only COPYs the one matching TARGETPLATFORM, so a multi-arch
# buildx build is seconds of copying with no QEMU. The SPA, the migrations and
# the zone database are embedded in the binary.
#
# distroless/static rather than scratch: the same no-shell, no-libc surface,
# plus CA certificates (outbound TLS to PostgreSQL, SMTP and the OIDC
# provider), /tmp and the nonroot user. Pinned by digest; Dependabot bumps it.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# buildx sets this per platform ("linux/amd64", "linux/arm64"). BINARY_ROOT
# defaults to the build-artifacts.sh layout; GoReleaser passes ".".
ARG TARGETPLATFORM
ARG BINARY_ROOT=dist/server

COPY ${BINARY_ROOT}/${TARGETPLATFORM}/vantigo /app/vantigo

# Stated explicitly although the :nonroot tag already sets it: CI asserts the
# image's User, and a base-image change must not be able to undo it.
USER 65532:65532

ENV PORT=8080
EXPOSE 8080

# The binary is its own probe client: the image has no shell or curl.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
  CMD ["/app/vantigo", "healthcheck"]

ENTRYPOINT ["/app/vantigo"]
CMD ["api"]
