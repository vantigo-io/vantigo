import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const entraCatalog = {
  en: {
    entra: "Microsoft Entra ID",
    entraDescription:
      "Connect your Microsoft Entra ID tenant, so the accounts of the people Staff registers are created, enabled and disabled there.",
  },
  nb: {
    entra: "Microsoft Entra ID",
    entraDescription:
      "Koble til Microsoft Entra ID-leietakeren deres, slik at kontoene til personene Ansatte registrerer, opprettes, aktiveres og deaktiveres der.",
  },
} satisfies CatalogResources;

registerCatalog("entra", entraCatalog);
