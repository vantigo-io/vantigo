import { createFileRoute } from "@tanstack/react-router";
import { GoogleConnectionTab } from "@vantigo/google-ui";

/** The steps: the project and the key, the upload, the delegation, the admin, the test and the web client. */
export const Route = createFileRoute("/workspace/google/connection")({
  component: GoogleConnectionTab,
});
