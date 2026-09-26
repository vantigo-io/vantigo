import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const invoicesCatalog = {
  en: {
    invoices: "Invoices",
    invoicesDescription: "Invoices and credit notes: drafts, and the numbered documents they are issued into.",
    failedToLoadMeta: "Could not load Invoices",
    noInvoices: "No invoices yet",
    noInvoicesDescription: "A draft becomes a numbered invoice the moment it is issued.",
  },
  nb: {
    invoices: "Fakturaer",
    invoicesDescription: "Fakturaer og kreditnotaer: utkast, og de nummererte dokumentene de utstedes som.",
    failedToLoadMeta: "Kunne ikke laste Fakturaer",
    noInvoices: "Ingen fakturaer ennå",
    noInvoicesDescription: "Et utkast blir en nummerert faktura i det øyeblikket det utstedes.",
  },
} satisfies CatalogResources;

registerCatalog("invoices", invoicesCatalog);
