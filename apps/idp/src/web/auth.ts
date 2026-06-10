import { createAuthClient } from "better-auth/react";
import { getVantigoConfig } from "./config";

const config = getVantigoConfig();

export const authClient = createAuthClient({
  baseURL: `${config.siteUrl}${config.sitePath}`,
});
