import { ActionIcon, Alert, Button, Card, Group, List, Stack, Text } from "@mantine/core";
import { IconAlertTriangle, IconPencil, IconReceipt } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
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
import { validNorwegianOrgNumber } from "../lib/norwegian-org-number";
import { CustomerBillingModal } from "./-customer-billing-modal";
import { CustomerEhfOffer, CustomerPeppolStatus } from "./-customer-peppol-status";
import "../i18n";

/**
 * The customer page's "Billing" card (design D4, D6): the profile GET
 * .../billing-profile always answers (a profile always exists conceptually,
 * with nothing decided for a customer that has none), shown read-only with
 * the warnings explained in words and the server's resolution rules mirrored
 * as hints under the fields they resolve. A plain `useQuery` with a skeleton
 * and an error branch, not `useSuspenseQuery`: its siblings on the Overview
 * tab load the same way, and a failing profile GET must cost this card
 * alone, not the whole tab. `canManageBilling` comes from the
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
  const { data: profile, isPending, isError, refetch } = useQuery(customerBillingProfileQueryOptions(customerId));
  const [modalOpened, setModalOpened] = useState(false);
  const warnings = (profile?.warnings ?? []).filter((code) => KNOWN_BILLING_WARNING_CODES.includes(code));

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group justify="space-between">
          <Group gap="xs">
            <IconReceipt size={18} stroke={1.5} />
            <Text fw={600} component="h3">
              {t("billing")}
            </Text>
          </Group>
          {/* Editing needs the profile the modal seeds itself from, so the
              action appears with it, not before. */}
          {canManageBilling && profile && (
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

        {/* The one code that is not a warning but an offer (design D4): teal,
            beside the yellow list rather than in it, and card-wide like it —
            switching to EHF changes the profile, not just the Peppol row. */}
        {profile && <CustomerEhfOffer customerId={customerId} profile={profile} canManageBilling={canManageBilling} />}

        {isPending ? (
          <ContentSkeleton rows={4} rowHeight={24} />
        ) : isError ? (
          // The same shape the timeline's own failure takes: what went wrong
          // and a way to ask again, inside the card.
          <Stack align="center" py="md">
            <Text c="red">{t("failedLoadBillingProfile")}</Text>
            <Button variant="light" onClick={() => refetch()}>
              {t("tryAgain")}
            </Button>
          </Stack>
        ) : (
          <BillingFields
            customer={customer}
            profile={profile}
            customerId={customerId}
            canManageBilling={canManageBilling}
          />
        )}
      </Stack>

      {profile && (
        <CustomerBillingModal
          opened={modalOpened}
          customerId={customerId}
          profile={profile}
          onClose={() => setModalOpened(false)}
        />
      )}
    </Card>
  );
};

/**
 * One row of the read-only definition list: a label, its value (or "not
 * set"), an optional resolution hint, and — for the Peppol ID row — whatever
 * else belongs beside that value: the Peppol answer and its Check EHF action
 * (design D6), which are about the participant this row names.
 */
const BillingRow = ({
  label,
  value,
  hint,
  extra,
}: {
  label: string;
  value: ReactNode;
  hint?: string | null;
  extra?: ReactNode;
}) => (
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
      {extra}
    </Stack>
  </Group>
);

/**
 * The eleven billing fields, in the same order the contract lists them.
 * Resolution hints mirror `resolveBillingProfile` in
 * `apps/server/internal/customers/directory.go`: an invoice email falls back
 * to the customer's own contact email, a reminder email falls back to the
 * resolved invoice email, and an EHF recipient is derived as
 * "0192:<organisation number>" for a business customer whose Norwegian legal
 * identity really carries an organisation number — the server derives
 * nothing from a legacy id that fails the mod-11 check, so neither does the
 * hint. No hint is shown when the identity is absent either: it is absent
 * whenever the viewer may not see it, and a derived recipient built from a
 * guess would be worse than none — and the group's default, which is the
 * only hint whose value the server resolved rather than this card.
 */
const BillingFields = ({
  customer,
  profile,
  customerId,
  canManageBilling,
}: {
  customer: CustomerResponse;
  profile: CustomerBillingProfile;
  customerId: number;
  canManageBilling?: boolean;
}) => {
  const { t, formatters } = useI18n("customers");
  const notSet = (
    <Text size="sm" c="dimmed">
      {t("billingNotSet")}
    </Text>
  );
  const value = (text: string | null) => (text === null ? notSet : <Text size="sm">{text}</Text>);
  const billRate = (rate: number) =>
    formatters.formatNumber(rate, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

  const contactEmail = customer.contactInfo?.email ?? null;
  const resolvedInvoiceEmail = profile.invoiceEmail ?? contactEmail;
  const invoiceEmailHint =
    profile.invoiceEmail === null && contactEmail ? t("billingInvoiceGoesTo", { email: contactEmail }) : null;
  const reminderEmailHint =
    profile.reminderEmail === null && resolvedInvoiceEmail
      ? t("billingRemindersGoTo", { email: resolvedInvoiceEmail })
      : null;
  const derivedPeppolId =
    profile.peppolId === null &&
    customer.type === "business" &&
    customer.identity?.country === "no" &&
    validNorwegianOrgNumber(customer.identity.id)
      ? `0192:${customer.identity.id}`
      : null;
  const peppolHint = derivedPeppolId ? t("billingEhfRecipientDerived", { id: derivedPeppolId }) : null;

  // Three states, one block: nothing to say when the customer is in no group or
  // its group decides nothing; where the term comes from when the profile
  // decided none; and which of the two applies when it did. The rule — own value
  // wins — is resolveBillingProfile's, and this card states it in words rather
  // than re-deriving it.
  const groupTerms = profile.groupDefault?.paymentTermsDays ?? null;
  const paymentTermsHint =
    groupTerms === null
      ? null
      : profile.paymentTermsDays === null
        ? t("billingInheritsTermsFromGroup", { count: groupTerms, group: profile.groupDefault?.group.name })
        : t("billingGroupTermsOverridden", { count: groupTerms });

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
        hint={paymentTermsHint}
      />
      <BillingRow label={t("billingCurrency")} value={value(profile.currency)} />
      <BillingRow
        label={t("billingDefaultBillRate")}
        value={
          profile.defaultBillRate === null ? (
            // Not "the invoicing default": an unset rate sends the chain on to
            // the project's or the person's rate, and the words say which.
            <Text size="sm" c="dimmed">
              {t("billingDefaultBillRateNotSet")}
            </Text>
          ) : (
            <Text size="sm">
              {/* The server refuses a rate without a currency, so the first
                  form should never show; if it ever does, the amount stands
                  alone rather than leaving a gap where the code would be. */}
              {profile.currency === null
                ? t("billingDefaultBillRateValueNoCurrency", { amount: billRate(profile.defaultBillRate) })
                : t("billingDefaultBillRateValue", {
                    amount: billRate(profile.defaultBillRate),
                    currency: profile.currency,
                  })}
            </Text>
          )
        }
      />
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
      <BillingRow
        label={t("billingPeppolId")}
        value={value(profile.peppolId)}
        hint={peppolHint}
        extra={
          <CustomerPeppolStatus
            customerId={customerId}
            profile={profile}
            identityId={customer.identity?.id ?? null}
            canManageBilling={canManageBilling}
          />
        }
      />
      <BillingRow label={t("billingGln")} value={value(profile.gln)} />
      <BillingRow label={t("billingBuyerReference")} value={value(profile.buyerReference)} />
    </Stack>
  );
};
