import { registerCatalog } from "@vantigo/frontend-shell";
import { productsCatalog } from "./i18n/catalog";

registerCatalog("products", productsCatalog);

export { productsCatalog } from "./i18n/catalog";
