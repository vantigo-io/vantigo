import { createFileRoute, notFound } from "@tanstack/react-router";
import { MeteringPointDetailsPage } from "@vantigo/energy-ui";
import { meteringPointQueryOptions } from "@vantigo/energy-ui/api/energy";
import { NotFoundError } from "@vantigo/energy-ui/api/request";

export const Route = createFileRoute("/$tenantSlug/energy/metering-points/$meteringPointId")({
  params: {
    parse: ({ meteringPointId }) => ({ meteringPointId: Number(meteringPointId) }),
    stringify: ({ meteringPointId }) => ({ meteringPointId: String(meteringPointId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(meteringPointQueryOptions(params.meteringPointId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: MeteringPointDetailsPage,
});
