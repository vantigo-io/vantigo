import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";

export interface AuthOptions {
  // biome-ignore lint/suspicious/noExplicitAny: Database instance is type-checked at runtime by drizzleAdapter
  db: any;
  secret: string;
  baseURL?: string;
}

/**
 * Creates a base Better-Auth instance.
 * Automatically maps to pluralized table names and uses custom AUTH_SECRET / AUTH_URL env vars.
 */
export const createVantigoAuth = (options: AuthOptions) => {
  return betterAuth({
    database: drizzleAdapter(options.db, {
      provider: "pg",
      usePlural: true, // Map to users, sessions, accounts, and verifications
    }),
    secret: options.secret,
    baseURL: options.baseURL,
    plugins: [
      // We will configure the OAuth 2.1 Provider Plugin here
    ],
  });
};
export type Auth = ReturnType<typeof createVantigoAuth>;
