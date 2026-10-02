import { Badge } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { invoicesCatalog } from "../i18n";

/** The colour each derived state is shown in (D10); a state a newer server added is grey. */
const colours: Record<string, string> = {
  draft: "gray",
  open: "blue",
  partially_paid: "yellow",
  overdue: "red",
  paid: "green",
  credited: "gray",
  issued: "gray",
};

/**
 * A document's derived state (D3) as a badge: grey draft, blue open, yellow
 * partially paid, red overdue, green paid, grey credited, and grey issued for
 * a credit note. The server judges the state; this only words and colours it.
 */
export const StateBadge = ({ state }: { state: string }) => {
  const { t } = useI18n("invoices");
  const key = `state.${state}`;
  return (
    <Badge variant="light" color={colours[state] ?? "gray"} data-testid="state-badge" data-state={state}>
      {key in invoicesCatalog.en ? t(key) : state}
    </Badge>
  );
};
