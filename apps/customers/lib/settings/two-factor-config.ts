import { config } from "@vantigo/customers/lib/config";
import type { TwoFactorConfig } from "./two-factor";

/**
 * Server-only accessor for the 2FA flags (lib/config must never be
 * imported from client components, so this lives apart from two-factor.ts).
 */
export function getTwoFactorConfig(): TwoFactorConfig {
  return {
    enabled: config.VANTIGO_CUSTOMERS_2FA_ENABLED,
    enforced: config.VANTIGO_CUSTOMERS_2FA_ENFORCED,
  };
}
