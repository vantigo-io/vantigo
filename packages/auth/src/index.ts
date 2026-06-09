import { betterAuth } from "better-auth";

/**
 * Creates a base Better-Auth instance.
 * The database adapter is injected so apps can use their own DB instances.
 */
// biome-ignore lint/suspicious/noExplicitAny: Database adapter types will be refined during implementation
export const createVantigoAuth = (databaseAdapter: any) => {
  return betterAuth({
    database: databaseAdapter,
    plugins: [
      // We will configure the OAuth 2.1 Provider Plugin here during Phase 2
    ],
  });
};
