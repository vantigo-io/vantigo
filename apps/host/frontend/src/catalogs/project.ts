import type { CatalogResources } from "@vantigo/frontend-shell";

const en = {
  "project.overviewTab": "Overview",
  "project.tasksTab": "Tasks",
  "project.peopleTab": "People",
  "project.billingTab": "Billing",
  "project.economyTab": "Economy",
  "project.timeTab": "Time",
  "project.views": "Project views",
  "project.createTaskTitle": "Create task",
  "project.taskProject": "Project",
  "project.taskProjectPlaceholder": "Search your projects",
  "project.taskProjectHint": "Pick the project the task belongs to, then fill in the task.",
  "project.noProjectsFound": "No projects found",
};

const nb: { [Key in keyof typeof en]: string } = {
  "project.overviewTab": "Oversikt",
  "project.tasksTab": "Oppgaver",
  "project.peopleTab": "Deltakere",
  "project.billingTab": "Fakturering",
  "project.economyTab": "Økonomi",
  "project.timeTab": "Timer",
  "project.views": "Prosjektvisninger",
  "project.createTaskTitle": "Opprett oppgave",
  "project.taskProject": "Prosjekt",
  "project.taskProjectPlaceholder": "Søk i prosjektene dine",
  "project.taskProjectHint": "Velg prosjektet oppgaven hører til, og fyll så ut oppgaven.",
  "project.noProjectsFound": "Fant ingen prosjekter",
};

export const projectCatalog = { en, nb } as const satisfies CatalogResources;
