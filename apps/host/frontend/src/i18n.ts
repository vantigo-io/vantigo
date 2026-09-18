import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";
// Each module package registers its own catalog when its i18n entry loads;
// the host only has to load them, through the package's public subpath,
// never through its source tree.
import "@vantigo/communications-ui/i18n";
import "@vantigo/customers-ui/i18n";
import "@vantigo/energy-ui/i18n";
import "@vantigo/products-ui/i18n";
import "@vantigo/projects-ui/i18n";
import { adminCatalog } from "./catalogs/admin";
import { authCatalog } from "./catalogs/auth";
import { commonCatalog } from "./catalogs/common";
import { customerCatalog } from "./catalogs/customer";
import { dashboardCatalog } from "./catalogs/dashboard";
import { errorCatalog } from "./catalogs/error";
import { legalCatalog } from "./catalogs/legal";
import { navigationCatalog } from "./catalogs/navigation";
import { projectCatalog } from "./catalogs/project";
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
    ...projectCatalog.en,
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
    ...projectCatalog.nb,
    ...adminCatalog.nb,
    ...settingsCatalog.nb,
    ...systemAdminCatalog.nb,
    ...legalCatalog.nb,
  },
} as const satisfies CatalogResources;

registerCatalog("host", hostCatalog);
