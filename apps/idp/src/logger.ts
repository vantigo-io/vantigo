import pino from "pino";

// Detect browser/edge environments to prevent loading Node transport modules
const isBrowser =
  typeof window !== "undefined" ||
  (typeof globalThis !== "undefined" &&
    !(
      "process" in globalThis &&
      "release" in
        (
          globalThis as typeof globalThis & {
            process: { release?: unknown };
          }
        ).process
    ));

const isDev =
  typeof process !== "undefined" && process.env?.NODE_ENV === "development";
const logLevel =
  (typeof process !== "undefined" && process.env?.LOG_LEVEL) || "info";

export const logger = pino({
  level: logLevel,
  ...(isBrowser
    ? {
        browser: {
          asObject: true,
          write: (o) => {
            // In Cloudflare, standard JSON output is automatically collected by dashboards/wrangler
            console.log(JSON.stringify(o));
          },
        },
      }
    : {
        transport: isDev
          ? {
              target: "pino-pretty",
              options: {
                colorize: true,
                translateTime: "HH:MM:ss Z",
                ignore: "pid,hostname",
              },
            }
          : undefined,
      }),
});
export type Logger = typeof logger;
export default logger;
