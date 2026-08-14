import { registerCatalog } from "@vantigo/frontend-shell";
import { communicationsCatalog } from "./catalog";

registerCatalog("communications", communicationsCatalog);

export { communicationsCatalog } from "./catalog";
