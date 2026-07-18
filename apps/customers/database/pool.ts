import { config } from "@vantigo/customers/lib/config";
import { Pool } from "pg";

export const pool = new Pool({
  connectionString: config.VANTIGO_CUSTOMERS_DATABASE_URL,
});
