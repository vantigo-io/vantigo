import { createFileRoute } from "@tanstack/react-router";
import { importsQueryOptions } from "@vantigo/customers-ui/api/imports";
import { ImportStartRoutePage } from "./-import-pages";

/** The start of an import (customers import wizard design D8): the dropzone and the recent imports. */
export const Route = createFileRoute("/customers/import/")({
  loader: ({ context: { queryClient } }) => queryClient.ensureQueryData(importsQueryOptions(1, 10)),
  component: ImportStartRoutePage,
});
