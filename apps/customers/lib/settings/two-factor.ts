/**
 * Pure helpers deriving the 2FA feature state from configuration.
 * Enforcement implies availability, so a config with only
 * VANTIGO_CUSTOMERS_2FA_ENFORCED=true still enables the feature.
 */

export interface TwoFactorConfig {
  enabled: boolean;
  enforced: boolean;
}

/** Whether 2FA features (settings section, verify pages) are available. */
export function isTwoFactorAvailable(config: TwoFactorConfig): boolean {
  return config.enabled || config.enforced;
}

/** Whether the signed-in user must be redirected to enrollment. */
export function requiresTwoFactorSetup(
  config: TwoFactorConfig,
  userTwoFactorEnabled: boolean | null | undefined,
): boolean {
  return config.enforced && !userTwoFactorEnabled;
}

/** Extracts the base32 secret from an otpauth:// URI for manual entry. */
export function totpSecretFromUri(totpUri: string): string | null {
  try {
    return new URL(totpUri).searchParams.get("secret");
  } catch {
    return null;
  }
}
