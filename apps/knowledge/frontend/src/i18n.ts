import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

/**
 * The `knowledge` catalog, flat keys, in English and Norwegian bokmål. The
 * app is Knowledge in English and Kunnskap in Norwegian (knowledge design K1);
 * the sidebar's sections are Workspace, Shared and Private (K4).
 */
export const knowledgeCatalog = {
  en: {
    knowledge: "Knowledge",
    navArticles: "Articles",
    navTrash: "Trash",

    homeTitle: "Knowledge",
    homeDescription: "The company's own routines, policies and how-tos.",
    articleTitle: "Article",
    untitled: "Untitled",
    trashTitle: "Trash",
    trashDescription: "Trashed articles are deleted for good after 30 days.",

    sectionWorkspace: "Workspace",
    sectionShared: "Shared",
    sectionPrivate: "Private",
    sectionFavourites: "Favourites",

    newArticle: "New article",

    notReadyTitle: "Not ready yet",
    notReadyDescription: "This part of Knowledge is being built.",
  },
  nb: {
    knowledge: "Kunnskap",
    navArticles: "Artikler",
    navTrash: "Papirkurv",

    homeTitle: "Kunnskap",
    homeDescription: "Bedriftens egne rutiner, retningslinjer og veiledninger.",
    articleTitle: "Artikkel",
    untitled: "Uten tittel",
    trashTitle: "Papirkurv",
    trashDescription: "Artikler i papirkurven slettes for godt etter 30 dager.",

    sectionWorkspace: "Arbeidsområde",
    sectionShared: "Delt",
    sectionPrivate: "Privat",
    sectionFavourites: "Favoritter",

    newArticle: "Ny artikkel",

    notReadyTitle: "Ikke klar ennå",
    notReadyDescription: "Denne delen av Kunnskap er under arbeid.",
  },
} satisfies CatalogResources;

registerCatalog("knowledge", knowledgeCatalog);
