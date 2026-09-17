import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "settings.passkeyUnsupported": "Passkeys are not supported on this device or browser.",
  "settings.passkeyEnrollmentCancelled": "Passkey enrollment was cancelled.",
  "settings.passkeyEnrollmentFailed": "Passkey enrollment could not be completed.",
  // The MFA enrolment notice (components/mfa-enrolment-notice.tsx). The
  // steps name the labels of the authenticator form below it.
  "settings.mfaRequiredTitle": "Set up two-factor authentication to continue",
  "settings.mfaRequiredWhy":
    "Your account is an administrator, and this installation requires two-factor authentication for administrator accounts. Until you have set it up, the rest of Vantigo is unavailable to you, and every other page will bring you back here.",
  "settings.mfaRequiredStep1":
    "Install an authenticator app on your phone if you do not have one already, for example Google Authenticator, Microsoft Authenticator or 1Password.",
  "settings.mfaRequiredStep2": "Under Authenticator app below, enter your current password and choose Begin setup.",
  "settings.mfaRequiredStep3":
    "Add the account to the authenticator app with the setup URI that appears: open it on the phone, or copy it into the app's add-account option.",
  "settings.mfaRequiredStep4":
    "Enter your current password once more together with the six-digit code the app shows, and choose Enable.",
  "settings.mfaRequiredStep5":
    "Save the recovery codes somewhere safe. They are shown only once, and they are your way in if you lose the phone.",
  "settings.mfaRequiredAfter":
    "Access to the rest of Vantigo is restored as soon as the authenticator is enabled. You do not need to sign out.",
};

const nb: { [Key in keyof typeof en]: string } = {
  "settings.passkeyUnsupported": "Passnøkler støttes ikke på denne enheten eller i denne nettleseren.",
  "settings.passkeyEnrollmentCancelled": "Registrering av passnøkkel ble avbrutt.",
  "settings.passkeyEnrollmentFailed": "Registrering av passnøkkel kunne ikke fullføres.",
  "settings.mfaRequiredTitle": "Sett opp tofaktorautentisering for å fortsette",
  "settings.mfaRequiredWhy":
    "Kontoen din er en administratorkonto, og denne installasjonen krever tofaktorautentisering for administratorkontoer. Til du har satt det opp, er resten av Vantigo utilgjengelig for deg, og alle andre sider sender deg tilbake hit.",
  "settings.mfaRequiredStep1":
    "Installer en autentiseringsapp på telefonen om du ikke allerede har en, for eksempel Google Authenticator, Microsoft Authenticator eller 1Password.",
  "settings.mfaRequiredStep2":
    "Under Autentiseringsapp nedenfor skriver du inn nåværende passord og velger Start oppsett.",
  "settings.mfaRequiredStep3":
    "Legg til kontoen i autentiseringsappen med oppsetts-URI-en som vises: åpne den på telefonen, eller kopier den inn i appens valg for å legge til konto.",
  "settings.mfaRequiredStep4":
    "Skriv inn nåværende passord én gang til sammen med den sekssifrede koden appen viser, og velg Aktiver.",
  "settings.mfaRequiredStep5":
    "Lagre gjenopprettingskodene et trygt sted. De vises bare én gang, og de er veien inn om du mister telefonen.",
  "settings.mfaRequiredAfter":
    "Tilgangen til resten av Vantigo gjenopprettes så snart autentiseringsappen er aktivert. Du trenger ikke å logge ut.",
};

export const settingsCatalog = { en, nb } as const satisfies CatalogResources;
