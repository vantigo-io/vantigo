import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The reminder settings (invoices payments and reminders design D7), and the regime and its review (D6). */
export type ReminderSettings = Schemas["InvoicesReminderSettings"];
export type ReminderSettingsInput = Schemas["InvoicesReminderSettingsRequest"];

export const reminderSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "reminder-settings"],
    queryFn: ({ signal }) => request<ReminderSettings>("/api/v1/invoices/settings/reminders", { signal }),
  });

/**
 * A full replace with the revision the settings were read at
 * (`invoices:manage`): every field is required, and a stale revision is a 409
 * without a code.
 */
export const updateReminderSettings = (input: ReminderSettingsInput): Promise<ReminderSettings> =>
  request<ReminderSettings>("/api/v1/invoices/settings/reminders", json("PUT", input));
