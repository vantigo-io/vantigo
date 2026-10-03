// @ts-check
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";
import { rehypeBaseLinks } from "./src/plugins/rehype-base-links.mjs";

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

/**
 * DOCS_BASE is the path the site is served under: "/" on docs.vantigo.io, and
 * "/docs" when scripts/docs-embed-overlay.sh builds the copy the server binary
 * embeds. DOCS_VERSION names the build in the banner: a release tag in the
 * image, "main@<sha>" on the public site, unset locally.
 */
const base = (process.env.DOCS_BASE ?? "/").replace(/\/+$/, "") || "/";
/**
 * DOCS_EMBED=1 builds the copy the server binary embeds: without the generated
 * API reference, which is 74 MB of pages that belong on the public site, and
 * without the link validator, which CI runs on the public build.
 */
const embedded = process.env.DOCS_EMBED === "1";
const version = process.env.DOCS_VERSION ?? "";

export default defineConfig({
  site: "https://docs.vantigo.io",
  base,
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
        ...(embedded ? [] : openAPISidebarGroups),
        {
          label: "Contributing",
          translations: { nb: "Bidra" },
          items: [{ autogenerate: { directory: "contributing" } }],
        },
      ],
      components: { Banner: "./src/components/Banner.astro" },
      plugins: [
        ...(embedded
          ? []
          : [
              starlightOpenAPI(
          contracts.map((name) => ({
            base: `reference/api/${name}`,
            schema: `../openapi/${name}.yaml`,
            label: name.charAt(0).toUpperCase() + name.slice(1),
            sidebar: { collapsed: true },
          })),
        ),
            ]),
        ...(base !== "/"
          ? []
          : [
        starlightLinksValidator({
          // The reference section is English only for now and nb falls back to
          // it; links from nb pages into the reference therefore point at /en/.
          errorOnFallbackPages: false,
          errorOnInconsistentLocale: false,
          // The installation guide legitimately points at http://localhost:8080.
          errorOnLocalLinks: false,
        }),
            ]),
      ],
    }),
  ],
  markdown: { rehypePlugins: [rehypeBaseLinks(base)] },
  vite: { define: { __DOCS_VERSION__: JSON.stringify(version) } },
});
