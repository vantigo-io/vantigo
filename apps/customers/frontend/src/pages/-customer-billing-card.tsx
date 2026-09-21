import { ActionIcon, Alert, Card, Group, List, Stack, Text } from "@mantine/core";
import { IconAlertTriangle, IconPencil, IconReceipt } from "@tabler/icons-react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useState } from "react";
import { type CustomerBillingProfile, customerBillingProfileQueryOptions } from "../api/billing-profile";
import type { CustomerResponse } from "../api/customers";
import {
  billingLanguageLabel,
  billingWarningMessage,
  deliveryMethodLabel,
  KNOWN_BILLING_WARNING_CODES,
} from "../lib/billing-labels";
import { CustomerBillingModal } from "./-customer-billing-modal";
import "../i18n";

/**
 * The customer page's "Billing" card (design D4, D6): the profile GET
 * .../billing-profile always answers (every field null for a customer that
 * has none — a profile always exists conceptually), shown read-only with the
 * warnings explained in words and the server's resolution rules mirrored as
 * hints under the fields they resolve. `canManageBilling` comes from the
 * host, which reads the caller's `customers:billing-manage` permission — a
 * narrower door than `canEdit`'s `customers:update` (design D1): the person
 * who may rename a customer is not thereby the person who may give it 90
 * days' credit. `customer` is passed down from the detail page rather than
 * fetched here, since the hints need its `contactInfo`, `type` and
 * `identity` (identity absent entirely when the viewer may not see it).
 */
export const CustomerBillingCard = ({
  customerId,
  customer,
  canManageBilling,
}: {
  customerId: number;
  customer: CustomerResponse;
  canManageBilling?: boolean;
}) => {
  const { t } = useI18n("customers");
  const { data: profile } = useSuspenseQuery(customerBillingProfileQueryOptions(customerId));
  const [modalOpened, setModalOpened] = useState(false);
  const warnings = profile.warnings.filter((code) => KNOWN_BILLING_WARNING_CODES.includes(code));

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group justify="space-between">
          <Group gap="xs">
            <IconReceipt size={18} stroke={1.5} />
            <Text fw={600}>{t("billing")}</Text>
          </Group>
          {canManageBilling && (
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t("editBillingProfile")}
              onClick={() => setModalOpened(true)}
            >
              <IconPencil size={16} />
            </ActionIcon>
          )}
        </Group>

        {warnings.length > 0 && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("billingWarningsTitle")}>
            <List size="sm" spacing="xs">
              {warnings.map((code) => (
                <List.Item key={code}>{billingWarningMessage(t, code)}</List.Item>
              ))}
            </List>
          </Alert>
        )}

        <BillingFields customer={customer} profile={profile} />
      </Stack>

      <CustomerBillingModal
        opened={modalOpened}
        customerId={customerId}
        profile={profile}
        onClose={() => setModalOpened(false)}
      />
    </Card>
  );
};

/** One row of the read-only definition list: a label, its value (or "not set"), and an optional resolution hint. */
const BillingRow = ({ label, value, hint }: { label: string; value: ReactNode; hint?: string | null }) => (
  <Group justify="space-between" align="flex-start" wrap="nowrap">
    <Text size="sm" c="dimmed" miw={140}>
      {label}
    </Text>
    <Stack gap={2} align="flex-end" style={{ textAlign: "right" }}>
      {value}
      {hint && (
        <Text size="xs" c="dimmed">
          {hint}
        </Text>
      )}
    </Stack>
  </Group>
);

/**
 * The ten billing fields, in the same order the contract lists them.
 * Resolution hints mirror `resolveBillingProfile` in
 * `apps/server/internal/customers/directory.go`: an invoice email falls back
 * to the customer's own contact email, a reminder email falls back to the
 * resolved invoice email, and an EHF recipient is derived as
 * "0192:<organisation number>" for a business customer with a Norwegian
 * legal identity. No hint is shown when the identity is absent — it is
 * absent whenever the viewer may not see it, and a derived recipient built
 * from a guess would be worse than none.
 */
const BillingFields = ({ customer, profile }: { customer: CustomerResponse; profile: CustomerBillingProfile }) => {
  const { t } = useI18n("customers");
  const notSet = (
    <Text size="sm" c="dimmed">
      {t("billingNotSet")}
    </Text>
  );
  const value = (text: string | null) => (text === null ? notSet : <Text size="sm">{text}</Text>);

  const contactEmail = customer.contactInfo?.email ?? null;
  const resolvedInvoiceEmail = profile.invoiceEmail ?? contactEmail;
  const invoiceEmailHint =
    profile.invoiceEmail === null && contactEmail ? t("billingInvoiceGoesTo", { email: contactEmail }) : null;
  const reminderEmailHint =
    profile.reminderEmail === null && resolvedInvoiceEmail
      ? t("billingRemindersGoTo", { email: resolvedInvoiceEmail })
      : null;
  const derivedPeppolId =
    profile.peppolId === null && customer.type === "business" && customer.identity?.country === "no"
      ? `0192:${customer.identity.id}`
      : null;
  const peppolHint = derivedPeppolId ? t("billingEhfRecipientDerived", { id: derivedPeppolId }) : null;

  return (
    <Stack gap="xs">
      <BillingRow label={t("billingInvoiceEmail")} value={value(profile.invoiceEmail)} hint={invoiceEmailHint} />
      <BillingRow label={t("billingReminderEmail")} value={value(profile.reminderEmail)} hint={reminderEmailHint} />
      <BillingRow
        label={t("billingPaymentTermsDays")}
        value={
          profile.paymentTermsDays === null ? (
            notSet
          ) : (
            <Text size="sm">{t("paymentTermsDaysValue", { count: profile.paymentTermsDays })}</Text>
          )
        }
      />
      <BillingRow label={t("billingCurrency")} value={value(profile.currency)} />
      <BillingRow
        label={t("billingLanguage")}
        value={profile.language === null ? notSet : <Text size="sm">{billingLanguageLabel(t, profile.language)}</Text>}
      />
      <BillingRow
        label={t("billingInvoiceDelivery")}
        value={
          profile.invoiceDelivery === null ? (
            notSet
          ) : (
            <Text size="sm">{deliveryMethodLabel(t, profile.invoiceDelivery)}</Text>
          )
        }
      />
      <BillingRow
        label={t("billingReminderDelivery")}
        value={
          profile.reminderDelivery === null ? (
            notSet
          ) : (
            <Text size="sm">{deliveryMethodLabel(t, profile.reminderDelivery)}</Text>
          )
        }
      />
      <BillingRow label={t("billingPeppolId")} value={value(profile.peppolId)} hint={peppolHint} />
      <BillingRow label={t("billingGln")} value={value(profile.gln)} />
      <BillingRow label={t("billingBuyerReference")} value={value(profile.buyerReference)} />
    </Stack>
  );
};
