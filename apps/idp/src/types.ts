import type { Database } from "./db";

export interface AppEnv {
  Variables: {
    db: Database;
  };
  Bindings: {
    HYPERDRIVE?: { connectionString: string };
    DATABASE_URL?: string;
  };
}
