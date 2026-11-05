import {
  Button,
  Card,
  Collapse,
  Group,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconChevronDown, IconChevronRight, IconUserCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { customerAddressesQueryOptions, customerContactsQueryOptions } from "../api/customers";
import { sitesQueryOptions } from "../api/deliveries";
import { metaQueryOptions } from "../api/meta";
import { customerProjectsQueryOptions } from "../api/projects";
import { salespeopleQueryOptions } from "../api/salespeople";
import type { CustomerSnapshot, Person } from "../api/types";
import type { OrdersAccess } from "../lib/access";
import { useOrdersFormat } from "../lib/format";
import { addressLine, CURRENCIES, type HeaderState } from "../lib/header";
import { CustomerPicker } from "./customer-picker";

/** Which parts of the header take edits now (O14, O26). */
export interface HeaderEditable {
  /** The customer, currency, VAT mode, terms and language: a draft's only. */
  draft: boolean;
  /** The delivery address, the contact and Deres referanse: also a confirmed order's, while it is not locked. */
  delivery: boolean;
}

export interface HeaderCardProps {
  /** A subscription has no delivery date. */
  kind: "quote" | "order" | "subscription";
  header: HeaderState;
  onChange: (patch: Partial<HeaderState>) => void;
  /** The document's customer as the server holds it; absent on a new document. */
  snapshot?: CustomerSnapshot;
  salesperson?: Person;
  editable: HeaderEditable;
  access: OrdersAccess;
  errors?: Record<string, string>;
}

const Field = ({ label, children }: { label: string; children: React.ReactNode }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed" tt="uppercase" fw={600} style={{ letterSpacing: "0.04em" }}>
      {label}
    </Text>
    <Text size="sm">{children || "—"}</Text>
  </Stack>
);

/**
 * The document's header (O13): the customer and the snapshot's delivery
 * address and contact, Deres referanse, the salesperson and the project,
 * the money (currency, prices inkl. or eks. mva, payment terms, whether the
 * discount prints), the dates and the texts. Each picker reads its module over
 * HTTP and is offered only with that module's key; without it the field shows
 * what the document holds.
 */
export const HeaderCard = ({
  kind,
  header,
  onChange,
  snapshot,
  salesperson,
  editable,
  access,
  errors = {},
}: HeaderCardProps) => {
  const { t } = useOrdersFormat();
  const [textsOpen, setTextsOpen] = useState(
    () => header.introText !== "" || header.closingText !== "" || header.internalNote !== "",
  );
  const customerId = header.customerId;
  const addresses = useQuery({
    ...customerAddressesQueryOptions(customerId),
    enabled: customerId > 0 && editable.delivery && access.canViewCustomers,
  });
  const contacts = useQuery({
    ...customerContactsQueryOptions(customerId),
    enabled: customerId > 0 && editable.delivery && access.canViewContacts,
  });
  const projects = useQuery({
    ...customerProjectsQueryOptions(customerId),
    enabled: customerId > 0 && editable.draft && access.canViewProjects,
  });
  // An order's site (O46), offered while Inventory keeps stock: the sites
  // that are active and fulfil orders, and the order's own whatever it is now.
  const meta = useQuery({ ...metaQueryOptions(), enabled: kind === "order" });
  const stockKept = kind === "order" && meta.data?.stockKept === true;
  const sites = useQuery({ ...sitesQueryOptions(), enabled: stockKept });
  const siteOptions = (sites.data?.items ?? [])
    .filter((site) => (site.active && site.fulfilsOrders) || site.id === header.siteId)
    .map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }));
  const sameCustomer = snapshot?.customerId === customerId;
  const addressOptions = (addresses.data ?? []).map((a) => ({
    value: String(a.id),
    label: [a.label, a.line1, a.line2, [a.postalCode, a.city].filter(Boolean).join(" ")].filter(Boolean).join(", "),
  }));
  if (
    sameCustomer &&
    header.deliveryAddressId !== null &&
    !addressOptions.some((o) => o.value === String(header.deliveryAddressId))
  )
    addressOptions.unshift({ value: String(header.deliveryAddressId), label: addressLine(snapshot?.deliveryAddress) });
  const contactOptions = (contacts.data ?? []).map((c) => ({
    value: String(c.contact.id),
    label: [`${c.contact.firstName} ${c.contact.lastName}`.trim(), c.email ?? c.contact.email]
      .filter(Boolean)
      .join(" · "),
  }));
  if (sameCustomer && header.contactId !== null && !contactOptions.some((o) => o.value === String(header.contactId)))
    contactOptions.unshift({
      value: String(header.contactId),
      label: [snapshot?.contact.name, snapshot?.contact.email].filter(Boolean).join(" · "),
    });
  const projectOptions = (projects.data ?? []).map((p) => ({
    value: String(p.id),
    label: p.code ? `${p.code} · ${p.name}` : p.name,
  }));
  if (header.projectId !== null && !projectOptions.some((o) => o.value === String(header.projectId)))
    projectOptions.unshift({ value: String(header.projectId), label: t("projectNumber", { id: header.projectId }) });
  const currencies = CURRENCIES.includes(header.currency as (typeof CURRENCIES)[number])
    ? [...CURRENCIES]
    : [header.currency, ...CURRENCIES];
  const isMe = access.userId !== undefined && header.salespersonId === access.userId;
  // The salespeople a draft may name: who holds a selling key (orders:access reads them).
  const salespeople = useQuery({ ...salespeopleQueryOptions(), enabled: editable.draft });
  const salespersonOptions = (salespeople.data ?? []).map((p) => ({ value: p.id, label: p.displayName }));
  if (header.salespersonId && !salespersonOptions.some((o) => o.value === header.salespersonId))
    salespersonOptions.unshift({
      value: header.salespersonId,
      label: header.salespersonChanged ? (access.userDisplayName ?? "—") : (salesperson?.displayName ?? "—"),
    });

  return (
    <Card withBorder padding="lg" data-testid="header-card">
      <Stack gap="md">
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md" verticalSpacing="sm">
          {editable.draft && access.canViewCustomers ? (
            <CustomerPicker
              value={customerId > 0 ? customerId : null}
              selected={snapshot ? { id: snapshot.customerId, name: snapshot.name } : undefined}
              clearable={false}
              required
              error={errors.customerId}
              onChange={(id) =>
                id !== null &&
                id !== customerId &&
                onChange({ customerId: id, deliveryAddressId: null, contactId: null, projectId: null })
              }
            />
          ) : (
            <Field label={t("customer")}>
              {snapshot
                ? snapshot.customerNumber
                  ? t("customerOption", { name: snapshot.name, number: snapshot.customerNumber })
                  : snapshot.name
                : ""}
            </Field>
          )}
          {editable.delivery && access.canViewCustomers ? (
            <Select
              label={t("deliveryAddress")}
              placeholder={t("deliveryAddressDefault")}
              data={addressOptions}
              value={header.deliveryAddressId === null ? null : String(header.deliveryAddressId)}
              onChange={(value) => onChange({ deliveryAddressId: value === null ? null : Number(value) })}
              clearable
              error={errors.deliveryAddressId}
            />
          ) : (
            <Field label={t("deliveryAddress")}>{addressLine(snapshot?.deliveryAddress)}</Field>
          )}
          {editable.delivery && access.canViewContacts ? (
            <Select
              label={t("contactPerson")}
              placeholder={t("contactNone")}
              data={contactOptions}
              value={header.contactId === null ? null : String(header.contactId)}
              onChange={(value) => onChange({ contactId: value === null ? null : Number(value) })}
              clearable
              error={errors.contactId}
            />
          ) : (
            <Field label={t("contactPerson")}>
              {[snapshot?.contact.name, snapshot?.contact.email].filter(Boolean).join(" · ")}
            </Field>
          )}
          {editable.delivery ? (
            <TextInput
              label={t("yourReference")}
              value={header.yourReference}
              maxLength={100}
              onChange={(e) => onChange({ yourReference: e.currentTarget.value })}
              error={errors.yourReference}
            />
          ) : (
            <Field label={t("yourReference")}>{header.yourReference}</Field>
          )}
          {editable.draft ? (
            <Stack gap={4}>
              <Select
                label={t("salesperson")}
                data={salespersonOptions}
                searchable
                value={header.salespersonId}
                onChange={(value) => value && onChange({ salespersonId: value, salespersonChanged: true })}
                error={errors.salespersonId}
              />
              {access.userId && !isMe && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  leftSection={<IconUserCheck size={12} />}
                  style={{ alignSelf: "flex-start" }}
                  onClick={() => onChange({ salespersonId: access.userId ?? null, salespersonChanged: true })}
                >
                  {t("makeMeSalesperson")}
                </Button>
              )}
            </Stack>
          ) : (
            <Field label={t("salesperson")}>{salesperson?.displayName ?? "—"}</Field>
          )}
          {editable.draft && access.canViewProjects ? (
            <Select
              label={t("project")}
              placeholder={t("projectNone")}
              data={projectOptions}
              value={header.projectId === null ? null : String(header.projectId)}
              onChange={(value) => onChange({ projectId: value === null ? null : Number(value) })}
              clearable
              error={errors.projectId}
            />
          ) : (
            header.projectId !== null && (
              <Field label={t("project")}>{t("projectNumber", { id: header.projectId })}</Field>
            )
          )}
        </SimpleGrid>

        <Group gap="lg" align="flex-end" wrap="wrap">
          <Select
            label={t("currency")}
            w={110}
            data={currencies}
            value={header.currency}
            allowDeselect={false}
            disabled={!editable.draft}
            onChange={(value) => value && onChange({ currency: value })}
            error={errors.currency}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("priceEntry")}
            </Text>
            <SegmentedControl
              aria-label={t("priceEntry")}
              disabled={!editable.draft}
              value={header.pricesIncludeVat ? "incl" : "excl"}
              onChange={(value) => onChange({ pricesIncludeVat: value === "incl" })}
              data={[
                { value: "excl", label: t("exclVat") },
                { value: "incl", label: t("inclVat") },
              ]}
            />
          </Stack>
          <NumberInput
            label={t("paymentTerms")}
            w={150}
            min={0}
            max={365}
            allowDecimal={false}
            suffix={` ${t("days")}`}
            disabled={!editable.draft}
            value={header.paymentTermsDays}
            onChange={(value) => onChange({ paymentTermsDays: value === "" ? "" : Number(value) })}
            error={errors.paymentTermsDays}
          />
          <Select
            label={t("documentLanguage")}
            w={140}
            data={[
              { value: "nb", label: t("languageNb") },
              { value: "en", label: t("languageEn") },
            ]}
            value={header.language}
            allowDeselect={false}
            disabled={!editable.draft}
            onChange={(value) => value && onChange({ language: value as "nb" | "en" })}
          />
          {kind === "quote" && (
            <DateInput
              label={t("validUntil")}
              w={170}
              clearable
              valueFormat={t("dateInputFormat")}
              disabled={!editable.draft}
              value={header.validUntil}
              onChange={(value) => onChange({ validUntil: value ?? null })}
              error={errors.validUntil}
            />
          )}
          {stockKept && (
            <Select
              label={t("site")}
              description={t("siteHint")}
              w={220}
              data={siteOptions}
              value={header.siteId === null ? null : String(header.siteId)}
              // A confirmed order's site changes to another, never to none (O46).
              clearable={editable.draft}
              disabled={!editable.delivery}
              onChange={(value) => onChange({ siteId: value ? Number(value) : editable.draft ? null : header.siteId })}
              error={errors.siteId}
              data-assist="orders.order.site"
            />
          )}
          {kind !== "subscription" && (
            <DateInput
              label={t("deliveryDate")}
              w={170}
              clearable
              valueFormat={t("dateInputFormat")}
              disabled={!editable.delivery}
              value={header.deliveryDate}
              onChange={(value) => onChange({ deliveryDate: value ?? null })}
              error={errors.deliveryDate}
            />
          )}
          <Switch
            label={t("discountVisible")}
            disabled={!editable.draft}
            checked={header.discountVisible}
            onChange={(e) => onChange({ discountVisible: e.currentTarget.checked })}
            mb={8}
          />
        </Group>

        <Stack gap="xs">
          <UnstyledButton onClick={() => setTextsOpen((open) => !open)} aria-expanded={textsOpen}>
            <Group gap={6}>
              {textsOpen ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
              <Title order={5}>{t("texts")}</Title>
            </Group>
          </UnstyledButton>
          <Collapse expanded={textsOpen}>
            <SimpleGrid cols={{ base: 1, md: 3 }}>
              <Textarea
                label={t("introText")}
                autosize
                minRows={3}
                maxLength={4000}
                disabled={!editable.draft}
                value={header.introText}
                onChange={(e) => onChange({ introText: e.currentTarget.value })}
              />
              <Textarea
                label={t("closingText")}
                autosize
                minRows={3}
                maxLength={4000}
                disabled={!editable.draft}
                value={header.closingText}
                onChange={(e) => onChange({ closingText: e.currentTarget.value })}
              />
              <Textarea
                label={t("internalNote")}
                description={t("internalNoteHint")}
                autosize
                minRows={3}
                maxLength={4000}
                disabled={!editable.draft}
                value={header.internalNote}
                onChange={(e) => onChange({ internalNote: e.currentTarget.value })}
              />
            </SimpleGrid>
          </Collapse>
        </Stack>
      </Stack>
    </Card>
  );
};
