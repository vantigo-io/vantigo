import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  tenantRequiredTitle: "Choose a workspace",
  tenantRequiredBody: "Your account is not connected to a workspace yet.",
  tenantUnavailableTitle: "Workspace unavailable",
  tenantUnavailableBody: "This workspace is unavailable right now.",
  tenantContactAdmin: "Contact your administrator for access.",
  tenantGoToSystemAdmin: "Go to system administration",
  tenantSelectorTitle: "Choose a workspace",
  tenantSelectorBody: "Select the workspace you want to open.",
  tenantSelectorUnavailable: "Unavailable",
  accessDeniedTitle: "You don't have access",
  accessDeniedBody: "This area is not available for your account in this workspace.",
  noAccessTitle: "No modules available",
  noAccessBody: "Your account has no access to any modules in this workspace yet.",
};

const nb: { [Key in keyof typeof en]: string } = {
  tenantRequiredTitle: "Velg et arbeidsområde",
  tenantRequiredBody: "Kontoen din er ikke koblet til et arbeidsområde ennå.",
  tenantUnavailableTitle: "Arbeidsområdet er ikke tilgjengelig",
  tenantUnavailableBody: "Dette arbeidsområdet er ikke tilgjengelig akkurat nå.",
  tenantContactAdmin: "Kontakt administratoren din for tilgang.",
  tenantGoToSystemAdmin: "Gå til systemadministrasjon",
  tenantSelectorTitle: "Velg et arbeidsområde",
  tenantSelectorBody: "Velg arbeidsområdet du vil åpne.",
  tenantSelectorUnavailable: "Ikke tilgjengelig",
  accessDeniedTitle: "Du har ikke tilgang",
  accessDeniedBody: "Dette området er ikke tilgjengelig for kontoen din i dette arbeidsområdet.",
  noAccessTitle: "Ingen moduler tilgjengelig",
  noAccessBody: "Kontoen din har ikke tilgang til noen moduler i dette arbeidsområdet ennå.",
};

export const tenantCatalog = { en, nb } as const satisfies CatalogResources;
