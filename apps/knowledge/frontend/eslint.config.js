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
  "@vantigo/invoices-ui",
  "@vantigo/invoices-ui/**",
  "@vantigo/expenses-ui",
  "@vantigo/expenses-ui/**",
  "@vantigo/calendar-ui",
  "@vantigo/calendar-ui/**",
  "@vantigo/entra-ui",
  "@vantigo/entra-ui/**",
  "@vantigo/google-ui",
  "@vantigo/google-ui/**",
  "@vantigo/staff-ui",
  "@vantigo/staff-ui/**",
  "@vantigo/assistant-ui",
  "@vantigo/assistant-ui/**",
  "@vantigo/planning-ui",
  "@vantigo/planning-ui/**",
  "@vantigo/inventory-ui",
  "@vantigo/inventory-ui/**",
  "@vantigo/orders-ui",
  "@vantigo/orders-ui/**",
  "@vantigo/manufacturing-ui",
  "@vantigo/manufacturing-ui/**",
  "@vantigo/accounting-ui",
  "@vantigo/accounting-ui/**",
  "@vantigo/agents-ui",
  "@vantigo/agents-ui/**",
  "@vantigo/app",
  "@vantigo/app/**",
  "../../../customers/**",
  "../../../communications/**",
  "../../../energy/**",
  "../../../products/**",
  "../../../projects/**",
  "../../../time/**",
  "../../../invoices/**",
  "../../../expenses/**",
  "../../../calendar/**",
  "../../../entra/**",
  "../../../google/**",
  "../../../staff/**",
  "../../../assistant/**",
  "../../../planning/**",
  "../../../inventory/**",
  "../../../orders/**",
  "../../../manufacturing/**",
  "../../../accounting/**",
  "../../../agents/**",
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
