import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "project.overviewTab": "Overview",
  "project.peopleTab": "People",
  "project.billingTab": "Billing",
  "project.views": "Project views",
};

const nb: { [Key in keyof typeof en]: string } = {
  "project.overviewTab": "Oversikt",
  "project.peopleTab": "Personer",
  "project.billingTab": "Fakturering",
  "project.views": "Prosjektvisninger",
};

export const projectCatalog = { en, nb } as const satisfies CatalogResources;
