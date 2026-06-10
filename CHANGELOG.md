# Changelog

## 1.0.0 (2026-06-10)


### Features

* add docker:build script task to automate compiling and image baking ([e2debaa](https://github.com/vantigo-io/vantigo/commit/e2debaacb7b2f4885c1936b1f212abfb9b42906e))
* **idp:** configure CORS, cookies, and Vantigo branding ([39712ca](https://github.com/vantigo-io/vantigo/commit/39712cab5e1c5acc3f5868a75c4e6eb004f3d8b2))
* **idp:** extract api endpoints to router and support cloudflare workers assets ([85e360b](https://github.com/vantigo-io/vantigo/commit/85e360bc499d513ff16dfa181d26c0501ed5bb0c))
* **idp:** implement cross-compilation compiler script and host-built distroless Docker image ([cf4b50b](https://github.com/vantigo-io/vantigo/commit/cf4b50b57ff9ca4c86c11feb92db7d6f553975b7))
* **idp:** implement IDP frontend layout, routing, and i18n support ([5cfb495](https://github.com/vantigo-io/vantigo/commit/5cfb495aafa7c6e79c0fcdd9d1526949eba9f373))
* **idp:** inject SITE_URL and SITE_PATH dynamically to client config ([d3f4077](https://github.com/vantigo-io/vantigo/commit/d3f40777097ccd35bbf492b29f0d3da8c4063dbc))
* **idp:** integrate Mantine Notifications and Vantigo Auth i18n ([8c5ed79](https://github.com/vantigo-io/vantigo/commit/8c5ed79fae15a8204d72387076c77eb4c070c4a0))
* **idp:** integrate pino logging, zod env validation, plural tables, and better-auth masking ([eb9900f](https://github.com/vantigo-io/vantigo/commit/eb9900f53460fa3ce4b68b85abe899f143b42b34))
* **idp:** restructure frontend routes into Next.js-style pages folders ([59d7d9b](https://github.com/vantigo-io/vantigo/commit/59d7d9b0e975335bfeaa1dcee3db82797f14bd74))
* **idp:** scaffold initial application structure and entry points ([bdd292c](https://github.com/vantigo-io/vantigo/commit/bdd292c466e35b46153f9dffe17fa7686b75bcb3))
* **idp:** serve logo.png as a favicon dynamically ([dadc48f](https://github.com/vantigo-io/vantigo/commit/dadc48f9e96351342982f2f60c4eaab0a385e297))
* **idp:** set up Drizzle schema, connection, and dependency injection ([74cb0f5](https://github.com/vantigo-io/vantigo/commit/74cb0f5dd6361727bb382ef691068e49043c041f))
* **idp:** support --migrate CLI argument for container database migrations ([651ddb2](https://github.com/vantigo-io/vantigo/commit/651ddb2042e97d0e39546f3db924cd70d56b3cb1))
* **idp:** support SITE_URL and SITE_PATH route prefixes ([3778075](https://github.com/vantigo-io/vantigo/commit/3778075f37fad78928b7837ccfd941f21cdeefeb))
* **idp:** tag Docker image with GHCR repository format and package version ([4407cf6](https://github.com/vantigo-io/vantigo/commit/4407cf601a603126b5f09e9947cc047ec0d438da))


### Bug Fixes

* **idp:** delegate static asset requests to downstream entry handlers ([ffd74bd](https://github.com/vantigo-io/vantigo/commit/ffd74bd084abd322303685deb8d05a580d31aa5e))
* **idp:** fix middleware registration and migration env validation ([be49739](https://github.com/vantigo-io/vantigo/commit/be497392f6e4ba28bc8bd1896cd2e5afd3d5e883))
* **idp:** remove Bun.env references from shared db index to prevent workerd crash ([90b0c79](https://github.com/vantigo-io/vantigo/commit/90b0c79817eac215530a5a1923b375a33d808f8a))
* **idp:** resolve client-side Better-Auth invalid base URL error ([5bbaad0](https://github.com/vantigo-io/vantigo/commit/5bbaad066c33d9d52b0a8ceedd9967896f999255))
