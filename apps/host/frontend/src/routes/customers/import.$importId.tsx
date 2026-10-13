import { createFileRoute, notFound } from "@tanstack/react-router";
import { importQueryOptions } from "@vantigo/customers-ui/api/imports";
import { NotFoundError } from "@vantigo/customers-ui/api/request";
import { ImportRoutePage } from "./-import-pages";

/**
 * One import (customers import wizard design D8): the stepper page, whose
 * step is the import's state. The URL names the import, so a refresh and a
 * shared link land on the same step.
 */
export const Route = createFileRoute("/customers/import/$importId")({
  params: {
    parse: ({ importId }) => ({ importId: Number(importId) }),
    stringify: ({ importId }) => ({ importId: String(importId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(importQueryOptions(params.importId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: ImportRoutePage,
});
