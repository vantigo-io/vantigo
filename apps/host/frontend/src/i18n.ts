import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";
import { communicationsCatalog } from "../../../communications/frontend/src/catalog";
import { customersCatalog } from "../../../customers/frontend/src/i18n";
import { energyCatalog } from "../../../energy/frontend/src/i18n";
import { productsCatalog } from "../../../products/frontend/src/i18n/catalog";
import { adminCatalog } from "./catalogs/admin";
import { authCatalog } from "./catalogs/auth";
import { commonCatalog } from "./catalogs/common";
import { customerCatalog } from "./catalogs/customer";
import { dashboardCatalog } from "./catalogs/dashboard";
import { errorCatalog } from "./catalogs/error";
import { legalCatalog } from "./catalogs/legal";
import { navigationCatalog } from "./catalogs/navigation";
import { settingsCatalog } from "./catalogs/settings";
import { systemAdminCatalog } from "./catalogs/system-admin";

export {
  getHostBuiltInRoleTranslation,
  getHostPermissionTranslation,
  hostBuiltInRoleTranslationKeys,
  hostPermissionTranslationKeys,
  translateHostPermissionCategory,
  translateHostPermissionDescription,
  translateHostPermissionKey,
  translateHostPermissionModule,
  translateHostRole,
  translateHostRoleDescription,
} from "./catalogs/admin";

export const hostCatalog = {
  en: {
    ...errorCatalog.en,
    ...navigationCatalog.en,
    ...commonCatalog.en,
    ...authCatalog.en,
    ...dashboardCatalog.en,
    ...customerCatalog.en,
    ...adminCatalog.en,
    ...settingsCatalog.en,
    ...systemAdminCatalog.en,
    ...legalCatalog.en,
  },
  nb: {
    ...errorCatalog.nb,
    ...navigationCatalog.nb,
    ...commonCatalog.nb,
    ...authCatalog.nb,
    ...dashboardCatalog.nb,
    ...customerCatalog.nb,
    ...adminCatalog.nb,
    ...settingsCatalog.nb,
    ...systemAdminCatalog.nb,
    ...legalCatalog.nb,
  },
} as const satisfies CatalogResources;

registerCatalog("host", hostCatalog);
registerCatalog("customers", customersCatalog);
registerCatalog("communications", communicationsCatalog);
registerCatalog("products", productsCatalog);
registerCatalog("energy", energyCatalog);
