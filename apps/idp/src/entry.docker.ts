import { serve } from "bun";
import { app } from "./app";

// Phase 2: We will inject generic bindings (Redis, Postgres) into the Hono context here.

const port = process.env.PORT || 3000;
console.log(`Starting Vantigo IDP (Docker Build) on http://localhost:${port}`);

serve({
  fetch: app.fetch,
  port,
});
