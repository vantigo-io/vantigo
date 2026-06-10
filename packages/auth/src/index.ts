import { i18n } from "@better-auth/i18n";
import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";

export interface AuthOptions {
  // biome-ignore lint/suspicious/noExplicitAny: Database instance is type-checked at runtime by drizzleAdapter
  db: any;
  secret: string;
  baseURL?: string;
  cookieDomain?: string;
}

/**
 * Creates a base Vantigo auth instance.
 * Automatically maps to pluralized table names and uses custom AUTH_SECRET / AUTH_URL env vars.
 */
export const createVantigoAuth = (options: AuthOptions) => {
  return betterAuth({
    database: drizzleAdapter(options.db, {
      provider: "pg",
      usePlural: true, // Map to users, sessions, accounts, and verifications
    }),
    secret: options.secret,
    baseURL: options.baseURL,
    advanced: {
      cookiePrefix: "vantigo",
      defaultCookieAttributes: options.cookieDomain
        ? {
            domain: options.cookieDomain,
          }
        : undefined,
    },
    plugins: [
      // We will configure the OAuth 2.1 Provider Plugin here
      i18n({
        translations: {
          no: {
            // Standard OAuth callback errors
            INVALID_CALLBACK_REQUEST:
              "Tilbakekallingsforespørselen er ugyldig. Sjekk konfigurasjonen for leverandøren.",
            INVALID_CODE:
              "Autorisasjonskoden er ugyldig eller har utløpt. Vennligst prøv å logge inn på nytt.",
            INTERNAL_SERVER_ERROR:
              "Det oppstod en uventet feil på serveren. Vennligst prøv igjen senere.",
            STATE_NOT_FOUND:
              "Sikkerhetsparameteren (state) ble ikke funnet. Vennligst prøv på nytt med informasjonskapsler aktivert.",
            STATE_INVALID:
              "Sikkerhetsparameteren (state) er ugyldig eller har utløpt.",
            STATE_MISMATCH:
              "Sikkerhetsparameteren (state) stemmer ikke overens. Forespørselen kan ha blitt avbrutt eller manipulert.",
            NO_CODE:
              "Ingen autorisasjonskode ble returnert fra innloggingsleverandøren.",
            NO_CALLBACK_URL:
              "Ingen tilbakekallings-URL ble oppgitt for forespørselen.",
            OAUTH_PROVIDER_NOT_FOUND:
              "Innloggingsleverandøren ble ikke funnet eller er ikke konfigurert.",
            EMAIL_NOT_FOUND:
              "E-postadressen ble ikke returnert fra innloggingsleverandøren. Sjekk personverninnstillingene dine hos leverandøren.",
            EMAIL_DOESNT_MATCH:
              "E-postadressen stemmer ikke overens med den registrerte e-posten på din eksisterende konto.",
            UNABLE_TO_GET_USER_INFO:
              "Kunne ikke hente profilinformasjon fra innloggingsleverandøren.",
            UNABLE_TO_LINK_ACCOUNT:
              "Kunne ikke koble sammen kontoer. E-postadressen kan allerede være i bruk med en annen innloggingsmetode.",
            UNABLE_TO_CREATE_USER:
              "Kunne ikke opprette en ny bruker i databasen.",
            UNABLE_TO_CREATE_SESSION:
              "Kunne ikke opprette en aktiv innloggingsøkt.",
            ACCOUNT_NOT_LINKED:
              "Denne eksterne kontoen er ikke koblet til en lokal bruker.",
            ACCOUNT_ALREADY_LINKED_TO_DIFFERENT_USER:
              "Denne eksterne kontoen er allerede koblet til en annen bruker.",
            SIGNUP_DISABLED:
              "Registrering av nye brukere er deaktivert på denne serveren.",
            // Standard credentials/auth errors
            USER_NOT_FOUND:
              "Ingen bruker ble funnet med den oppgitte e-postadressen.",
            INVALID_EMAIL_OR_PASSWORD: "Feil e-postadresse eller passord.",
            INVALID_PASSWORD: "Det oppgitte passordet er ugyldig.",
            EMAIL_ALREADY_IN_USE:
              "E-postadressen er allerede registrert hos oss.",
            INVALID_EMAIL: "E-postadressen har et ugyldig format.",
            PASSWORD_TOO_SHORT:
              "Passordet er for kort. Det må være minst 8 tegn langt.",
            USER_ALREADY_EXISTS: "Brukeren eksisterer allerede.",
            FAILED_TO_CREATE_USER:
              "Kunne ikke opprette brukeren. Vennligst prøv igjen.",
            FAILED_TO_CREATE_SESSION: "Kunne ikke opprette en innloggingsøkt.",
            SESSION_EXPIRED:
              "Innloggingsøkten har utløpt. Vennligst logg inn på nytt.",
            INVALID_SESSION: "Innloggingsøkten er ugyldig.",
          },
        },
      }),
    ],
  });
};
export type Auth = ReturnType<typeof createVantigoAuth>;
