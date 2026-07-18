import { config } from "@vantigo/customers/lib/config";
import { createAuthClient } from "better-auth/react";

export const authClient = createAuthClient({
  baseURL: config.VANTIGO_CUSTOMERS_BASE_URL,
});
