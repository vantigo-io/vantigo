// The page's copy, in English and Norwegian bokmål, chosen by the browser's
// language (API access design K29). The page stands alone, so it carries its
// own two catalogs rather than the shell's.

export type Copy = {
  title: string;
  keyLabel: string;
  keyHint: string;
  use: string;
  clear: string;
  loading: string;
  session: string;
  key: string;
  noAccess: string;
  guide: string;
  refused: (code: string) => string;
  failed: string;
};

// The reference section is English only, so both languages link the English page.
export const INTRODUCTION_URL = "https://docs.vantigo.io/en/reference/api-introduction/";

const en: Copy = {
  title: "Vantigo API reference",
  keyLabel: "API key",
  keyHint: "The key stays in this page only. It is never stored, and it is gone when you leave or clear it.",
  use: "Use key",
  clear: "Clear",
  loading: "Loading the API document…",
  session:
    "You are signed in, so this shows every operation an API key can call. Without a key, “Test Request” sends your own session and is judged as your call. Enter a key to see what it reaches and to call as it.",
  key: "Showing what this key reaches. “Test Request” sends the key.",
  noAccess:
    "Sign in to Vantigo, or enter an API key, to see the API reference. An Owner makes keys under Workspace admin › API access.",
  guide: "How to use the API",
  refused: (code) =>
    `The key was refused (${code}). Check that it is typed in full and has not expired or been revoked.`,
  failed: "The API document could not be loaded. Try again.",
};

const nb: Copy = {
  title: "Vantigo API-referanse",
  keyLabel: "API-nøkkel",
  keyHint:
    "Nøkkelen holdes bare i denne siden. Den lagres aldri, og den er borte når du går herfra eller tømmer feltet.",
  use: "Bruk nøkkel",
  clear: "Tøm",
  loading: "Henter API-dokumentet …",
  session:
    "Du er logget inn, så her vises alle operasjoner en API-nøkkel kan kalle. Uten nøkkel sender «Test Request» din egen økt, og kallet vurderes som ditt. Skriv inn en nøkkel for å se hva den når, og for å kalle som den.",
  key: "Viser det denne nøkkelen når. «Test Request» sender nøkkelen.",
  noAccess:
    "Logg inn i Vantigo, eller skriv inn en API-nøkkel, for å se API-referansen. En eier lager nøkler under Administrer arbeidsområde › API-tilgang.",
  guide: "Slik bruker du API-et",
  refused: (code) =>
    `Nøkkelen ble avvist (${code}). Sjekk at hele nøkkelen er skrevet inn, og at den ikke er utløpt eller trukket tilbake.`,
  failed: "API-dokumentet kunne ikke hentes. Prøv igjen.",
};

/** Norwegian for nb, nn and no; English otherwise. */
export function copyFor(language: string | undefined): { copy: Copy; lang: string } {
  const primary = (language ?? "").toLowerCase().split("-")[0];
  if (primary === "nb" || primary === "nn" || primary === "no") {
    return { copy: nb, lang: "nb" };
  }
  return { copy: en, lang: "en" };
}
