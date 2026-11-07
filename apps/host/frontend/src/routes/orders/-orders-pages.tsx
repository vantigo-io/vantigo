import { useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import type { OrdersAccess } from "@vantigo/orders-ui";
import { AnalysisPage } from "@vantigo/orders-ui/pages/analysis";
import { DeliveriesPage } from "@vantigo/orders-ui/pages/deliveries";
import { DeliveryEditorPage } from "@vantigo/orders-ui/pages/delivery-editor";
import { OrderEditorPage } from "@vantigo/orders-ui/pages/order-editor";
import { OrdersPage } from "@vantigo/orders-ui/pages/orders";
import { QuoteEditorPage } from "@vantigo/orders-ui/pages/quote-editor";
import { QuotesPage } from "@vantigo/orders-ui/pages/quotes";
import { SettingsPage } from "@vantigo/orders-ui/pages/settings";
import { SubscriptionEditorPage } from "@vantigo/orders-ui/pages/subscription-editor";
import { SubscriptionsPage } from "@vantigo/orders-ui/pages/subscriptions";
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
export const AnalysisRoutePage = withAccess((access) => <AnalysisPage access={access} />);
export const SubscriptionsRoutePage = withAccess((access) => <SubscriptionsPage access={access} />);
export const DeliveriesRoutePage = withAccess((access) => <DeliveriesPage access={access} />);

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
  const { customerId, productId } = useSearch({ from: "/orders/orders/new" });
  return <OrderEditorPage customerId={customerId} productId={productId} access={access} />;
};

export const QuoteRoutePage = withAccess((access) => <QuoteEditor access={access} />);
export const NewQuoteRoutePage = withAccess((access) => <NewQuote access={access} />);
export const OrderRoutePage = withAccess((access) => <OrderEditor access={access} />);
export const NewOrderRoutePage = withAccess((access) => <NewOrder access={access} />);

const SubscriptionEditor = ({ access }: { access: OrdersAccess }) => {
  const { subscriptionId } = useParams({ from: "/orders/subscriptions/$subscriptionId" });
  return <SubscriptionEditorPage subscriptionId={Number(subscriptionId)} access={access} />;
};
const NewSubscription = ({ access }: { access: OrdersAccess }) => {
  const { customerId } = useSearch({ from: "/orders/subscriptions/new" });
  return <SubscriptionEditorPage customerId={customerId} access={access} />;
};

export const SubscriptionRoutePage = withAccess((access) => <SubscriptionEditor access={access} />);
export const NewSubscriptionRoutePage = withAccess((access) => <NewSubscription access={access} />);

const DeliveryEditor = ({ access }: { access: OrdersAccess }) => {
  const { deliveryId } = useParams({ from: "/orders/deliveries/$deliveryId" });
  return <DeliveryEditorPage deliveryId={Number(deliveryId)} access={access} />;
};

export const DeliveryRoutePage = withAccess((access) => <DeliveryEditor access={access} />);
