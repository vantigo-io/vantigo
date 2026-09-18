import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "customer.overviewTab": "Overview",
  "customer.energyTab": "Energy",
  "customer.projectsTab": "Projects",
  "customer.openInInbox": "Open in inbox",
  "customer.views": "Customer views",
};

const nb: { [Key in keyof typeof en]: string } = {
  "customer.overviewTab": "Oversikt",
  "customer.energyTab": "Energi",
  "customer.projectsTab": "Prosjekter",
  "customer.openInInbox": "Åpne i innboksen",
  "customer.views": "Kundevisninger",
};

export const customerCatalog = { en, nb } as const satisfies CatalogResources;
