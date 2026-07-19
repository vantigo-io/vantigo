/**
 * Runs once when the server starts (never during `next build`).
 * Eagerly validates the runtime configuration so a misconfigured container
 * fails fast at boot with a readable error instead of on the first request.
 */
export async function register() {
  try {
    const { config } = await import("@vantigo/customers/lib/config");
    // Any property access triggers full schema validation.
    config.VANTIGO_CUSTOMERS_BASE_URL;
    console.log("[startup] Environment configuration is valid.");
  } catch (error) {
    // Next would log the error but keep serving; a misconfigured container
    // must terminate instead.
    console.error(error instanceof Error ? error.message : error);
    process.exit(1);
  }
}
