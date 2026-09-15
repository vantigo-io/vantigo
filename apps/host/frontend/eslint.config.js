import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

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
      // The host composes module packages through their public exports
      // (@vantigo/<module>-ui and its subpaths). Reaching into a sibling
      // workspace's source tree bypasses that contract.
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["../../../**"],
              message: "Import module frontends through their package name, not their source tree.",
            },
          ],
        },
      ],
    },
  },
  {
    // TanStack Router route files export a `Route` object rather than the component
    // itself; the router plugin handles HMR for these files.
    files: ["src/routes/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
]);
