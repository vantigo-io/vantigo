/**
 * Runs once per runtime when the server starts (never during `next build`).
 * The actual validation lives in instrumentation.node.ts and is only loaded
 * in the Node.js runtime — the Edge bundle must stay free of Node APIs.
 */
export async function register() {
  if (process.env.NEXT_RUNTIME === "nodejs") {
    const { validateConfigOrExit } = await import("./instrumentation.node");
    await validateConfigOrExit();
  }
}
