import { createTheme, localStorageColorSchemeManager, type MantineColorsTuple } from "@mantine/core";

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
 * Mantine's neutral `dark` scale, replaced by a blue-tinted grey so the dark
 * theme keeps the cool feel of the light page (#f7f8fc). Mantine reads shade 7
 * as the surface, 0 as the text, 2 as dimmed text and 4 as borders, so these ten
 * values place every Mantine component. theme.css pins the page background
 * (shade 8), the surface, the text and the link to the same values;
 * theme.test.ts keeps the two in step.
 */
export const vantigoDark: MantineColorsTuple = [
  "#dde1ef",
  "#b4bad0",
  "#8c93ad",
  "#666d8a",
  "#3d4361",
  "#2f3450",
  "#262a41",
  "#1c1f32",
  "#15182a",
  "#0f1120",
];

/**
 * Where the chosen colour scheme is mirrored in local storage: "light", "dark"
 * or "auto". The pre-paint snippet in apps/server/internal/web/index.go reads
 * the same key, so keep the two equal.
 */
export const COLOR_SCHEME_STORAGE_KEY = "vantigo-color-scheme";

/** Pass to the MantineProvider as `colorSchemeManager`, with `defaultColorScheme="auto"`. */
export const vantigoColorSchemeManager = localStorageColorSchemeManager({ key: COLOR_SCHEME_STORAGE_KEY });

/**
 * The semantic colour tokens theme.css defines for both colour schemes. Use
 * these, or a Mantine variable, in styles; never write a colour literal in a
 * component.
 */
export const vantigoTokens = {
  bodyBg: "var(--vantigo-body-bg)",
  surface: "var(--vantigo-surface)",
  text: "var(--vantigo-text)",
  textDimmed: "var(--vantigo-text-dimmed)",
  border: "var(--vantigo-border)",
  link: "var(--vantigo-link)",
  subtleBg: "var(--vantigo-subtle-bg)",
} as const;

/**
 * The shared Mantine theme for all Vantigo frontend apps. Pass it to the
 * MantineProvider in each app's entry point, alongside importing
 * `@vantigo/frontend-shell/theme.css` for the global styles.
 */
export const vantigoTheme = createTheme({
  colors: { vantigo: vantigoIndigo, dark: vantigoDark },
  primaryColor: "vantigo",
  // Mantine's default dark shade (8) turns indigo muddy on dark grey.
  primaryShade: { light: 6, dark: 6 },
  defaultRadius: "md",
});
