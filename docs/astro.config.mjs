// @ts-check
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";

const repository = "https://github.com/vantigo-io/vantigo";

/**
 * Every module's OpenAPI contract becomes a generated API reference. The list
 * mirrors openapi/*.yaml (common.yaml holds shared components, not a document).
 */
const contracts = [
  "identity",
  "customers",
  "communications",
  "products",
  "energy",
  "projects",
  "time",
  "expenses",
  "invoices",
];

export default defineConfig({
  site: "https://docs.vantigo.io",
  integrations: [
    starlight({
      title: "Vantigo",
      description: "Documentation for Vantigo, the open-source platform for running your business.",
      logo: { src: "./src/assets/logo.png", alt: "Vantigo" },
      favicon: "/favicon.png",
      defaultLocale: "en",
      locales: {
        en: { label: "English", lang: "en" },
        nb: { label: "Norsk bokmål", lang: "nb-NO" },
      },
      social: [{ icon: "github", label: "GitHub", href: repository }],
      editLink: { baseUrl: `${repository}/edit/main/docs/` },
      lastUpdated: true,
      customCss: ["./src/styles/custom.css"],
      sidebar: [
        {
          label: "User guide",
          translations: { nb: "Brukerveiledning" },
          items: [{ autogenerate: { directory: "user" } }],
        },
        {
          label: "Administration",
          translations: { nb: "Administrasjon" },
          items: [{ autogenerate: { directory: "admin" } }],
        },
        {
          label: "Reference",
          translations: { nb: "Referanse" },
          items: [{ autogenerate: { directory: "reference" } }],
        },
        ...openAPISidebarGroups,
        {
          label: "Contributing",
          translations: { nb: "Bidra" },
          items: [{ autogenerate: { directory: "contributing" } }],
        },
      ],
      plugins: [
        starlightOpenAPI(
          contracts.map((name) => ({
            base: `reference/api/${name}`,
            schema: `../openapi/${name}.yaml`,
            label: name.charAt(0).toUpperCase() + name.slice(1),
            sidebar: { collapsed: true },
          })),
        ),
        starlightLinksValidator({
          // The reference section is English only for now and nb falls back to
          // it; links from nb pages into the reference therefore point at /en/.
          errorOnFallbackPages: false,
          errorOnInconsistentLocale: false,
          // The installation guide legitimately points at http://localhost:8080.
          errorOnLocalLinks: false,
        }),
      ],
    }),
  ],
});
