import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "customer.overviewTab": "Overview",
  "customer.energyTab": "Energy",
  "customer.correspondenceTab": "Correspondence",
};

const nb: { [Key in keyof typeof en]: string } = {
  "customer.overviewTab": "Oversikt",
  "customer.energyTab": "Energi",
  "customer.correspondenceTab": "Korrespondanse",
};

export const customerCatalog = { en, nb } as const satisfies CatalogResources;
