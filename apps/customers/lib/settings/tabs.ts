export const SETTINGS_TABS = ["general", "security"] as const;

export type SettingsTab = (typeof SETTINGS_TABS)[number];

/** Maps a ?tab= query value to a valid tab, falling back to general. */
export function resolveSettingsTab(value: string | null): SettingsTab {
  return (SETTINGS_TABS as readonly string[]).includes(value ?? "")
    ? (value as SettingsTab)
    : "general";
}
