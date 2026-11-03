import { OutlookConnectionCard } from "@vantigo/entra-ui";
import type { ConnectionCardProps } from "../lib/connections";

/** Entra's card speaks its callback's `outlook=` words; the host hands it the neutral outcome in them. */
export const EntraConnectionCard = ({ outcome, onReturn, onOffered }: ConnectionCardProps) => (
  <OutlookConnectionCard
    outcome={outcome ? { outlook: outcome.outcome, ...(outcome.code ? { code: outcome.code } : {}) } : undefined}
    onReturn={onReturn}
    onOffered={onOffered}
  />
);
