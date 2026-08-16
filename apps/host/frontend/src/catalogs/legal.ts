import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "legal.copied": "Copied!",
  "legal.clickToCopy": "Click to copy.",
  "legal.retrievedFrom": "Retrieved from {{source}}",
};

const nb: { [Key in keyof typeof en]: string } = {
  "legal.copied": "Kopiert!",
  "legal.clickToCopy": "Klikk for å kopiere.",
  "legal.retrievedFrom": "Hentet fra {{source}}",
};

export const legalCatalog = { en, nb } as const satisfies CatalogResources;
