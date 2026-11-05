import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

/**
 * The `orders` catalog, flat keys, in English and Norwegian bokmål. The
 * Norwegian words are the trade's: Ordre, tilbud, ordrebekreftelse, mal,
 * Deres referanse, inkl. mva (orders design O11, O19).
 */
export const ordersCatalog = {
  en: {
    orders: "Orders",
    navQuotes: "Quotes",
    navOrders: "Orders",
    navTemplates: "Templates",
    navSettings: "Settings",

    quotesTitle: "Quotes",
    quotesDescription: "The quotes made for customers: drafts, sent and answered.",
    quoteTitle: "Quote",
    newQuoteTitle: "New quote",
    salesOrdersTitle: "Orders",
    salesOrdersDescription: "The customers' orders: drafts, confirmed and their delivery and invoicing.",
    salesOrderTitle: "Order",
    newSalesOrderTitle: "New order",
    templatesTitle: "Quote templates",
    templatesDescription: "Ready-made texts and lines a new quote can start from.",
    settingsTitle: "Orders settings",
    settingsDescription: "Number series, validity, payment terms, the send channel and the mail texts.",

    customerPanelTitle: "Quotes and orders",
    newQuote: "New quote",
    newSalesOrder: "New order",

    notReadyTitle: "Not ready yet",
    notReadyDescription: "This part of Orders is being built.",
  },
  nb: {
    orders: "Ordre",
    navQuotes: "Tilbud",
    navOrders: "Ordre",
    navTemplates: "Maler",
    navSettings: "Innstillinger",

    quotesTitle: "Tilbud",
    quotesDescription: "Tilbudene til kundene: utkast, sendte og besvarte.",
    quoteTitle: "Tilbud",
    newQuoteTitle: "Nytt tilbud",
    salesOrdersTitle: "Ordre",
    salesOrdersDescription: "Kundenes ordrer: utkast, bekreftede og hvordan de leveres og faktureres.",
    salesOrderTitle: "Ordre",
    newSalesOrderTitle: "Ny ordre",
    templatesTitle: "Tilbudsmaler",
    templatesDescription: "Ferdige tekster og linjer et nytt tilbud kan starte fra.",
    settingsTitle: "Innstillinger for Ordre",
    settingsDescription: "Nummerserier, gyldighet, betalingsfrist, avsenderkanal og e-posttekster.",

    customerPanelTitle: "Tilbud og ordrer",
    newQuote: "Nytt tilbud",
    newSalesOrder: "Ny ordre",

    notReadyTitle: "Ikke klar ennå",
    notReadyDescription: "Denne delen av Ordre er under arbeid.",
  },
} satisfies CatalogResources;

registerCatalog("orders", ordersCatalog);
