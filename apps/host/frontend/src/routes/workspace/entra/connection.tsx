import { createFileRoute } from "@tanstack/react-router";
import { EntraConnectionTab } from "@vantigo/entra-ui";

/** Steps 1 to 3: register the application, add a client secret, grant the permissions and test the connection. */
export const Route = createFileRoute("/workspace/entra/connection")({
  component: EntraConnectionTab,
});
