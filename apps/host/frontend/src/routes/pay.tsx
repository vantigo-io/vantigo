import { createFileRoute } from "@tanstack/react-router";
import { PayPage } from "@vantigo/invoices-ui/pages/pay";

/**
 * The public pay page (invoices payments and reminders design D14, D15): a
 * pay link's `/pay?token=…`, and Vipps' return to `/pay?attempt=…&nonce=…`.
 * `/pay` is in `publicPaths`, so the root asks for no session, authorization
 * or profile here — on the public host every request the page makes must be
 * on `security.PayRoutes()`'s allowlist (`pay.test.tsx` holds it to that).
 */
export const Route = createFileRoute("/pay")({
  component: PayPage,
});
