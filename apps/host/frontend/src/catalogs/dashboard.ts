import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "dashboard.title": "Dashboard",
  "dashboard.customers": "Customers",
  "dashboard.communications": "Communications",
  "dashboard.products": "Products",
  "dashboard.chooseModule": "Choose a module to get started.",
  "dashboard.manageCustomers": "Manage customers and contacts.",
  "dashboard.reviewMessages": "Review and send messages.",
  "dashboard.manageProducts": "Manage products, prices and categories.",
  "dashboard.open": "Open",
};

const nb: { [Key in keyof typeof en]: string } = {
  "dashboard.title": "Kontrollpanel",
  "dashboard.customers": "Kunder",
  "dashboard.communications": "Kommunikasjon",
  "dashboard.products": "Produkter",
  "dashboard.chooseModule": "Velg en modul for å komme i gang.",
  "dashboard.manageCustomers": "Administrer kunder og kontakter.",
  "dashboard.reviewMessages": "Se gjennom og send meldinger.",
  "dashboard.manageProducts": "Administrer produkter, priser og kategorier.",
  "dashboard.open": "Åpne",
};

export const dashboardCatalog = { en, nb } as const satisfies CatalogResources;
