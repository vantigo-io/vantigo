import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "settings.passkeyUnsupported": "Passkeys are not supported on this device or browser.",
  "settings.passkeyEnrollmentCancelled": "Passkey enrollment was cancelled.",
  "settings.passkeyEnrollmentFailed": "Passkey enrollment could not be completed.",
};

const nb: { [Key in keyof typeof en]: string } = {
  "settings.passkeyUnsupported": "Passnøkler støttes ikke på denne enheten eller i denne nettleseren.",
  "settings.passkeyEnrollmentCancelled": "Registrering av passnøkkel ble avbrutt.",
  "settings.passkeyEnrollmentFailed": "Registrering av passnøkkel kunne ikke fullføres.",
};

export const settingsCatalog = { en, nb } as const satisfies CatalogResources;
