import { db } from "@vantigo/customers/database/db";
import { config } from "@vantigo/customers/lib/config";
import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";
import { twoFactor } from "better-auth/plugins";

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
    sendResetPassword: async ({ user, url }) => {
      // TODO: Send a real email once an email provider is configured.
      console.log(`[auth] Password reset requested for ${user.email}: ${url}`);
    },
  },
  user: {
    additionalFields: {
      language: {
        type: "string",
        required: false,
        input: false,
      },
      preferredCustomerType: {
        type: "string",
        required: false,
        input: false,
      },
      lastCustomerType: {
        type: "string",
        required: false,
        input: false,
      },
    },
  },
  telemetry: {
    enabled: false,
  },
  plugins: [
    twoFactor({
      issuer: config.VANTIGO_CUSTOMERS_2FA_ISSUER_NAME,
    }),
  ],
  rateLimit: {
    // Disabled in e2e tests; enabled everywhere else.
    enabled: config.VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED,
  },
});
