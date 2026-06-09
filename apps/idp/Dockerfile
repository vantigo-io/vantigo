# Use the minimal Google Distroless CC runtime image (nonroot)
FROM gcr.io/distroless/cc-debian12:nonroot

WORKDIR /app

# The TARGETARCH argument is injected automatically by Docker Buildx (e.g., 'amd64', 'arm64')
# We default to 'arm64' for local Apple Silicon Mac builds if BuildKit/Buildx is not used.
ARG TARGETARCH=arm64

# Copy the pre-compiled standalone binary from the host's dist/ directory
COPY dist/vantigo-idp-linux-${TARGETARCH} /app/vantigo-idp

# Copy the migrations directory from the monorepo root
COPY apps/idp/src/db/migrations /app/db/migrations

# Set environment variables
ENV NODE_ENV=production
ENV PORT=3000

# Expose the application port
EXPOSE 3000

# Run the compiled standalone Hono server
ENTRYPOINT ["/app/vantigo-idp"]
