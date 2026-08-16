import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  tenantRequiredTitle: "Choose a workspace",
  tenantRequiredBody: "Your account is not connected to a workspace yet.",
  tenantUnavailableTitle: "Workspace unavailable",
  tenantUnavailableBody: "This workspace is unavailable right now.",
  tenantContactAdmin: "Contact your administrator for access.",
};

const nb: { [Key in keyof typeof en]: string } = {
  tenantRequiredTitle: "Velg et arbeidsområde",
  tenantRequiredBody: "Kontoen din er ikke koblet til et arbeidsområde ennå.",
  tenantUnavailableTitle: "Arbeidsområdet er ikke tilgjengelig",
  tenantUnavailableBody: "Dette arbeidsområdet er ikke tilgjengelig akkurat nå.",
  tenantContactAdmin: "Kontakt administratoren din for tilgang.",
};

export const tenantCatalog = { en, nb } as const satisfies CatalogResources;
