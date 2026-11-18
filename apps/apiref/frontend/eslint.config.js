import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import globals from "globals";
import tseslint from "typescript-eslint";

// The API reference page is a static shell of its own (API access design
// K27): it imports no module frontend, the host app or the shell.
export default defineConfig([
  globalIgnores(["dist"]),
  {
    files: ["**/*.ts"],
    extends: [js.configs.recommended, tseslint.configs.recommended],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@vantigo/*", "../../../*/**"],
              message: "The API reference page stands alone: it imports no other workspace.",
            },
          ],
        },
      ],
    },
  },
]);
