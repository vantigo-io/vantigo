import { createFileRoute } from "@tanstack/react-router";
import { importsQueryOptions } from "@vantigo/energy-ui/api/imports";
import { ImportStartRoutePage } from "../-import-pages";

/** The start of a metering-point import (energy import design D9): the dropzone and the recent imports. */
export const Route = createFileRoute("/energy/metering-points/import/")({
  loader: ({ context: { queryClient } }) => queryClient.ensureQueryData(importsQueryOptions(1, 10)),
  component: ImportStartRoutePage,
});
