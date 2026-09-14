import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "systemAdmin.settings": "Settings",
  "systemAdmin.manageWorkspace": "Manage this workspace.",
  "systemAdmin.overview": "Overview",
  "systemAdmin.users": "Users",
  "systemAdmin.invitations": "Invitations",
  "systemAdmin.roles": "Roles",
  "systemAdmin.settingsSection": "Settings section",
  "systemAdmin.controlPlane": "System status",
  "systemAdmin.title": "System admin",
  "systemAdmin.description": "Control maintenance mode for the whole system.",
  "systemAdmin.maintenanceMode": "Maintenance mode",
  "systemAdmin.maintenanceModeDescription":
    "Temporarily block non-administrators while system maintenance is in progress.",
  "systemAdmin.maintenanceEnabled": "Enable maintenance mode",
  "systemAdmin.maintenanceMessage": "Message shown to users",
  "systemAdmin.saveMaintenance": "Save maintenance settings",
  "systemAdmin.maintenanceSaved": "Maintenance settings saved",
  "systemAdmin.maintenanceSavedBody": "The system status was updated.",
  "systemAdmin.maintenanceSaveFailed": "Could not save maintenance settings",
  "systemAdmin.maintenanceActive": "Maintenance mode is active",
  "systemAdmin.maintenanceActiveBody": "System administrators can continue working.",
};

const nb: { [Key in keyof typeof en]: string } = {
  "systemAdmin.settings": "Innstillinger",
  "systemAdmin.manageWorkspace": "Administrer dette arbeidsområdet.",
  "systemAdmin.overview": "Oversikt",
  "systemAdmin.users": "Brukere",
  "systemAdmin.invitations": "Invitasjoner",
  "systemAdmin.roles": "Roller",
  "systemAdmin.settingsSection": "Innstillingsseksjon",
  "systemAdmin.controlPlane": "Systemstatus",
  "systemAdmin.title": "Systemadministrator",
  "systemAdmin.description": "Kontroller vedlikeholdsmodus for hele systemet.",
  "systemAdmin.maintenanceMode": "Vedlikeholdsmodus",
  "systemAdmin.maintenanceModeDescription":
    "Blokker midlertidig tilgang for andre enn administratorer under vedlikehold.",
  "systemAdmin.maintenanceEnabled": "Aktiver vedlikeholdsmodus",
  "systemAdmin.maintenanceMessage": "Melding som vises til brukere",
  "systemAdmin.saveMaintenance": "Lagre vedlikeholdsinnstillinger",
  "systemAdmin.maintenanceSaved": "Vedlikeholdsinnstillinger lagret",
  "systemAdmin.maintenanceSavedBody": "Systemstatusen ble oppdatert.",
  "systemAdmin.maintenanceSaveFailed": "Kunne ikke lagre vedlikeholdsinnstillingene",
  "systemAdmin.maintenanceActive": "Vedlikeholdsmodus er aktiv",
  "systemAdmin.maintenanceActiveBody": "Systemadministratorer kan fortsette arbeidet.",
};

export const systemAdminCatalog = { en, nb } as const satisfies CatalogResources;
