import * as accounting from "@vantigo/accounting-ui/api/request";
import * as agents from "@vantigo/agents-ui/api/request";
import * as assistant from "@vantigo/assistant-ui/api/request";
import * as calendar from "@vantigo/calendar-ui/api/request";
import * as communications from "@vantigo/communications-ui/api/request";
import * as customers from "@vantigo/customers-ui/api/request";
import * as energy from "@vantigo/energy-ui/api/request";
import * as entra from "@vantigo/entra-ui/api/request";
import * as expenses from "@vantigo/expenses-ui/api/request";
import * as google from "@vantigo/google-ui/api/request";
import * as inventory from "@vantigo/inventory-ui/api/request";
import * as invoices from "@vantigo/invoices-ui/api/request";
import * as knowledge from "@vantigo/knowledge-ui/api/request";
import * as manufacturing from "@vantigo/manufacturing-ui/api/request";
import * as meta from "@vantigo/meta-ui/api/request";
import * as orders from "@vantigo/orders-ui/api/request";
import * as planning from "@vantigo/planning-ui/api/request";
import * as products from "@vantigo/products-ui/api/request";
import * as projects from "@vantigo/projects-ui/api/request";
import * as staff from "@vantigo/staff-ui/api/request";
import * as time from "@vantigo/time-ui/api/request";
import * as host from "./request";

type Callback = (() => void | Promise<void>) | undefined;

type SessionClient = {
  request: <T>(url: string) => Promise<T>;
  setAuthStateClearer: (clearer: Callback) => void;
  setUnauthorizedHandler: (handler: Callback) => void;
};

/**
 * Every API client in the running app, keyed by the package that owns it. Each
 * module package builds its own client with `createApiClient`, so a 401 from a
 * module endpoint reaches the host only once the host has handed that client
 * its callbacks too. A new module package's client is added here.
 */
export const sessionClients: Record<string, SessionClient> = {
  "@vantigo/app": host,
  "@vantigo/accounting-ui": accounting,
  "@vantigo/assistant-ui": assistant,
  "@vantigo/calendar-ui": calendar,
  "@vantigo/communications-ui": communications,
  "@vantigo/customers-ui": customers,
  "@vantigo/energy-ui": energy,
  "@vantigo/entra-ui": entra,
  "@vantigo/expenses-ui": expenses,
  "@vantigo/google-ui": google,
  "@vantigo/inventory-ui": inventory,
  "@vantigo/invoices-ui": invoices,
  "@vantigo/knowledge-ui": knowledge,
  "@vantigo/manufacturing-ui": manufacturing,
  "@vantigo/meta-ui": meta,
  "@vantigo/agents-ui": agents,
  "@vantigo/orders-ui": orders,
  "@vantigo/planning-ui": planning,
  "@vantigo/products-ui": products,
  "@vantigo/projects-ui": projects,
  "@vantigo/staff-ui": staff,
  "@vantigo/time-ui": time,
};

/**
 * Hands the host's session handling to every client: on a 401, clear the
 * cached session, then send the person to sign in again. `undefined` clears both.
 */
export const setSessionHandlers = (handlers?: { clearAuthState: Callback; onUnauthorized: Callback }) => {
  for (const client of Object.values(sessionClients)) {
    client.setAuthStateClearer(handlers?.clearAuthState);
    client.setUnauthorizedHandler(handlers?.onUnauthorized);
  }
};
