import { type CatalogResources, registerCatalog } from "@vantigo/frontend-shell";

export const calendarCatalog = {
  en: {
    calendar: "Calendar",
    calendarDescription: "Your appointments and what the other apps have put in your days.",
    today: "Today",
    previous: "Previous",
    next: "Next",
    view: "View",
    viewMonth: "Month",
    viewWeek: "Week",
    viewDay: "Day",
    viewList: "List",
    newAppointment: "New appointment",
    newAppointmentComingSoon: "Creating appointments from the grid is not built yet: {{range}}.",
    appointment: "Appointment",
    appointmentComingSoon: "The appointment sheet is not built yet: {{title}}.",
  },
  nb: {
    calendar: "Kalender",
    calendarDescription: "Avtalene dine og det de andre appene har lagt inn i dagene dine.",
    today: "I dag",
    previous: "Forrige",
    next: "Neste",
    view: "Visning",
    viewMonth: "Måned",
    viewWeek: "Uke",
    viewDay: "Dag",
    viewList: "Liste",
    newAppointment: "Ny avtale",
    newAppointmentComingSoon: "Å opprette avtaler fra kalenderen er ikke bygget ennå: {{range}}.",
    appointment: "Avtale",
    appointmentComingSoon: "Avtalearket er ikke bygget ennå: {{title}}.",
  },
} satisfies CatalogResources;

registerCatalog("calendar", calendarCatalog);
