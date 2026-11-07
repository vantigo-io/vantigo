import { assistTargets as customers } from "@vantigo/customers-ui/assist-targets";
import { assistTargets as energy } from "@vantigo/energy-ui/assist-targets";
import { assistTargets as expenses } from "@vantigo/expenses-ui/assist-targets";
import type { AssistTarget } from "@vantigo/frontend-shell";
import { assistTargets as inventory } from "@vantigo/inventory-ui/assist-targets";
import { assistTargets as invoices } from "@vantigo/invoices-ui/assist-targets";
import { assistTargets as knowledge } from "@vantigo/knowledge-ui/assist-targets";
import { assistTargets as orders } from "@vantigo/orders-ui/assist-targets";
import { assistTargets as projects } from "@vantigo/projects-ui/assist-targets";
import { assistTargets as time } from "@vantigo/time-ui/assist-targets";

/**
 * Every module frontend's tour targets (design S11), collected here because the
 * host composes: no module package imports another, and the assistant package
 * imports none of them. The server's copy (apps/server/internal/assistant/
 * assist-targets.json) is generated from the same files, so the model can name
 * only what is in this list. A module without targets simply has no entry.
 */
export const assistTargets: readonly AssistTarget[] = [
  ...customers,
  ...energy,
  ...expenses,
  ...inventory,
  ...invoices,
  ...knowledge,
  ...orders,
  ...projects,
  ...time,
];
