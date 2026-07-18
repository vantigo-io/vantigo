import { db } from "@vantigo/customers/database/db";
import { config } from "@vantigo/customers/lib/config";
import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";

export const auth = betterAuth({
  appName: "Vantigo - Customers",
  baseURL: config.VANTIGO_CUSTOMERS_BASE_URL,
  secret: config.VANTIGO_CUSTOMERS_AUTH_SECRET,
  database: drizzleAdapter(db, {
    provider: "pg",
    usePlural: true,
  }),
  emailAndPassword: {
    enabled: config.VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED,
  },
  user: {
    additionalFields: {
      language: {
        type: "string",
        required: false,
        input: false,
      },
    },
  },
  telemetry: {
    enabled: false,
  },
});
