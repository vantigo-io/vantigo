import type { Database } from "./db";

export interface AppEnv {
  Variables: {
    db: Database;
  };
  Bindings: {
    HYPERDRIVE?: { connectionString: string };
    DATABASE_URL?: string;
    AUTH_SECRET?: string;
    SITE_URL?: string;
    SITE_PATH?: string;
    AUTH_URL?: string;
  };
}
