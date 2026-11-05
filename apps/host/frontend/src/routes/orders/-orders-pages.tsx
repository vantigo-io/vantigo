import { useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import type { OrdersAccess } from "@vantigo/orders-ui";
import { OrderEditorPage } from "@vantigo/orders-ui/pages/order-editor";
import { OrdersPage } from "@vantigo/orders-ui/pages/orders";
import { QuoteEditorPage } from "@vantigo/orders-ui/pages/quote-editor";
import { QuotesPage } from "@vantigo/orders-ui/pages/quotes";
import { SettingsPage } from "@vantigo/orders-ui/pages/settings";
import { TemplatesPage } from "@vantigo/orders-ui/pages/templates";
import type { ReactNode } from "react";
import { useOrdersAccess } from "../../lib/orders-access";

/**
 * The Orders app's pages, each told what the caller may do (orders design
 * §8) once the root layout's session has answered. They sit beside the route
 * files because a route file may export nothing but its `Route`.
 */
const withAccess = (render: (access: OrdersAccess) => ReactNode) =>
  function OrdersRoutePage() {
    const { ready, access } = useOrdersAccess();
    if (!ready) return <ContentSkeleton rows={4} rowHeight={48} />;
    return <>{render(access)}</>;
  };

export const QuotesRoutePage = withAccess((access) => <QuotesPage access={access} />);
export const OrdersRoutePage = withAccess((access) => <OrdersPage access={access} />);
export const TemplatesRoutePage = withAccess((access) => <TemplatesPage access={access} />);
export const SettingsRoutePage = withAccess((access) => <SettingsPage access={access} />);

const QuoteEditor = ({ access }: { access: OrdersAccess }) => {
  const { quoteId } = useParams({ from: "/orders/quotes/$quoteId" });
  return <QuoteEditorPage quoteId={Number(quoteId)} access={access} />;
};
const NewQuote = ({ access }: { access: OrdersAccess }) => {
  const { customerId } = useSearch({ from: "/orders/quotes/new" });
  return <QuoteEditorPage customerId={customerId} access={access} />;
};
const OrderEditor = ({ access }: { access: OrdersAccess }) => {
  const { orderId } = useParams({ from: "/orders/orders/$orderId" });
  return <OrderEditorPage orderId={Number(orderId)} access={access} />;
};
const NewOrder = ({ access }: { access: OrdersAccess }) => {
  const { customerId } = useSearch({ from: "/orders/orders/new" });
  return <OrderEditorPage customerId={customerId} access={access} />;
};

export const QuoteRoutePage = withAccess((access) => <QuoteEditor access={access} />);
export const NewQuoteRoutePage = withAccess((access) => <NewQuote access={access} />);
export const OrderRoutePage = withAccess((access) => <OrderEditor access={access} />);
export const NewOrderRoutePage = withAccess((access) => <NewOrder access={access} />);
