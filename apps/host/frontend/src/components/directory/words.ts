import { ApiValidationError } from "../../api/request";

/** A translation function, as `useI18n` hands one out. */
export type Translate = (key: string, values?: Record<string, unknown>) => string;

/** The directory refusals the host has words for. */
const worded = new Set(["no_directory", "provider_unreachable"]);

/**
 * A directory read's refusal in the Owner's words: `no_directory` and `provider_unreachable` by their code, never the
 * server's English; a validation error by its first field's sentence; anything else by its own message.
 */
export const directoryRefusal = (error: unknown, t: Translate): string => {
  const code = (error as { code?: unknown } | null)?.code;
  if (typeof code === "string" && worded.has(code)) return t(`admin.directoryRefusal_${code}`);
  if (error instanceof ApiValidationError) return Object.values(error.fieldErrors)[0] ?? error.message;
  return error instanceof Error ? error.message : String(error);
};
