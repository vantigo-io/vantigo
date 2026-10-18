import { createFileRoute, notFound } from "@tanstack/react-router";
import { meteringPointQueryOptions } from "@vantigo/energy-ui/api/energy";
import { NotFoundError } from "@vantigo/energy-ui/api/request";
import { MeteringPointDetailsRoutePage } from "../-metering-point-details";

export const Route = createFileRoute("/energy/metering-points/$meteringPointId")({
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
  component: MeteringPointDetailsRoutePage,
});
