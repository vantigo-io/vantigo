import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

const forbiddenModuleImports = [
  "@vantigo/customers-ui",
  "@vantigo/customers-ui/**",
  "@vantigo/communications-ui",
  "@vantigo/communications-ui/**",
  "@vantigo/energy-ui",
  "@vantigo/energy-ui/**",
  "@vantigo/products-ui",
  "@vantigo/products-ui/**",
  "@vantigo/projects-ui",
  "@vantigo/projects-ui/**",
  "@vantigo/time-ui",
  "@vantigo/time-ui/**",
  "@vantigo/app",
  "@vantigo/app/**",
  "../../../customers/**",
  "../../../communications/**",
  "../../../energy/**",
  "../../../products/**",
  "../../../projects/**",
  "../../../time/**",
  "../../../host/**",
];

export default defineConfig([
  globalIgnores(["dist"]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: forbiddenModuleImports,
              message: "Module frontends must not import other module frontends or the host app.",
            },
          ],
        },
      ],
    },
  },
]);
