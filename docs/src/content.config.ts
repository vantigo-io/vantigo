import { defineCollection, z } from "astro:content";
import { docsLoader, i18nLoader } from "@astrojs/starlight/loaders";
import { docsSchema, i18nSchema } from "@astrojs/starlight/schema";

export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: z.object({
        // Repository paths this page documents. tools/docs/check-coverage.ts
        // fails a change under one of these paths that touches no page listing
        // it, which is what keeps the documentation in step with the code.
        sources: z.array(z.string()).optional(),
      }),
    }),
  }),
  // UI string overrides per locale live in src/content/i18n/<locale>.json.
  i18n: defineCollection({ loader: i18nLoader(), schema: i18nSchema() }),
};
