import { createTheme, type MantineColorsTuple } from "@mantine/core";

/**
 * The Vantigo brand color scale, anchored on indigo #4f46e5 (shade 6).
 * Change it here and every app picks it up.
 */
const vantigoIndigo: MantineColorsTuple = [
  "#eef2ff",
  "#e0e7ff",
  "#c7d2fe",
  "#a5b4fc",
  "#818cf8",
  "#6366f1",
  "#4f46e5",
  "#4338ca",
  "#3730a3",
  "#312e81",
];

/**
 * The shared Mantine theme for all Vantigo frontend apps. Pass it to the
 * MantineProvider in each app's entry point, alongside importing
 * `@vantigo/frontend-shell/theme.css` for the global styles.
 */
export const vantigoTheme = createTheme({
  colors: { vantigo: vantigoIndigo },
  primaryColor: "vantigo",
  defaultRadius: "md",
});
