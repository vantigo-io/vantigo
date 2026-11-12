import { createFileRoute, notFound } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { LotPage } from "@vantigo/inventory-ui/pages/lot";
import { moduleLabelKey } from "../../apps";
import { isPositiveId } from "./-ids";
import "../../i18n";

/** A lot's page with another module's name — a hold's or the lot's source — as the sidebar labels it. */
const LotRoute = () => {
  const { t } = useI18n("host");
  return (
    <LotPage
      moduleLabel={(module) => {
        const key = moduleLabelKey(module);
        return key ? t(key) : module;
      }}
    />
  );
};

/** One lot's page (manufacturing phase 2 M61). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/lots/$lotId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.lotId)) throw notFound();
  },
  component: LotRoute,
});
