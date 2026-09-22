import { Anchor, Group, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { CustomerRegistryAddress, CustomerRegistryRecord } from "../api/registry";
import { countryDisplayName } from "../lib/country-display";
import "../i18n";

/**
 * One row of the Registry card's read-only definition list — the same
 * label/value shape the Billing card's own `BillingRow` uses, so the two
 * cards on the Overview tab read as one page rather than two designs.
 */
const RegistryRow = ({ label, value }: { label: string; value: ReactNode }) => (
  <Group justify="space-between" align="flex-start" wrap="nowrap">
    <Text size="sm" c="dimmed" miw={140}>
      {label}
    </Text>
    <Stack gap={2} align="flex-end" style={{ textAlign: "right" }}>
      {value}
    </Stack>
  </Group>
);

/**
 * An address exactly as the registry holds it (design D1): its own free-form
 * lines first, then the post code and city, the municipality it names, and
 * the country. Rendered, never merged into the customer's own addresses — a
 * registry address is offered beside the addresses section (design D3), and
 * only a person's click turns it into one.
 */
const RegistryAddress = ({ address }: { address: CustomerRegistryAddress }) => {
  const { locale } = useI18n("customers");
  const postal = [address.postalCode, address.city].filter(Boolean).join(" ");
  return (
    <Stack gap={0} align="flex-end">
      {address.lines.map((line) => (
        <Text key={line} size="sm">
          {line}
        </Text>
      ))}
      {postal && <Text size="sm">{postal}</Text>}
      {address.municipality && (
        <Text size="xs" c="dimmed">
          {address.municipality}
        </Text>
      )}
      {address.countryCode && (
        <Text size="xs" c="dimmed">
          {countryDisplayName(address.countryCode.toLocaleLowerCase(), locale)}
        </Text>
      )}
    </Stack>
  );
};

/**
 * The registry writes a website down as it was given to it — "www.brreg.no",
 * with no scheme — which a browser would resolve against this app's own
 * origin. A missing scheme is filled in rather than the link being dropped.
 */
const websiteHref = (website: string) => (/^https?:\/\//i.test(website) ? website : `https://${website}`);

/**
 * What Enhetsregisteret holds about this entity, field by field (design D5).
 * A field the registry has nothing for is left out entirely rather than
 * shown as "not set": this is a record of facts about the world, not a form
 * someone has yet to fill in — the one exception being VAT registration,
 * which the registry always answers, and whose "no" is a fact in itself.
 */
export const CustomerRegistryFields = ({ record }: { record: CustomerRegistryRecord }) => {
  const { t, formatters } = useI18n("customers");
  // The code alone means nothing to a reader and the description alone
  // cannot be looked up, so the two are shown together when both are there.
  const industry =
    record.industryCode && record.industry
      ? `${record.industryCode} — ${record.industry}`
      : (record.industry ?? record.industryCode);

  return (
    <Stack gap="xs">
      {record.organisationForm && (
        <RegistryRow label={t("registryOrganisationForm")} value={<Text size="sm">{record.organisationForm}</Text>} />
      )}
      {industry && <RegistryRow label={t("registryIndustry")} value={<Text size="sm">{industry}</Text>} />}
      {record.employees !== null && (
        <RegistryRow
          label={t("registryEmployees")}
          value={<Text size="sm">{formatters.formatNumber(record.employees)}</Text>}
        />
      )}
      <RegistryRow
        label={t("registryVatRegistered")}
        value={<Text size="sm">{record.vatRegistered ? t("yes") : t("no")}</Text>}
      />
      {record.foundedOn && (
        <RegistryRow
          label={t("registryFoundedOn")}
          value={<Text size="sm">{formatters.formatDate(record.foundedOn)}</Text>}
        />
      )}
      {record.website && (
        <RegistryRow
          label={t("website")}
          value={
            <Anchor href={websiteHref(record.website)} target="_blank" rel="noopener noreferrer" size="sm">
              {record.website}
            </Anchor>
          }
        />
      )}
      {record.email && (
        <RegistryRow
          label={t("email")}
          value={
            <Anchor href={`mailto:${record.email}`} size="sm">
              {record.email}
            </Anchor>
          }
        />
      )}
      {record.phone && (
        <RegistryRow
          label={t("phone")}
          value={
            <Anchor href={`tel:${record.phone.replace(/\s+/g, "")}`} size="sm">
              {record.phone}
            </Anchor>
          }
        />
      )}
      {record.mobile && (
        <RegistryRow
          label={t("registryMobile")}
          value={
            <Anchor href={`tel:${record.mobile.replace(/\s+/g, "")}`} size="sm">
              {record.mobile}
            </Anchor>
          }
        />
      )}
      {/* The parent's number and nothing more: it may well belong to no
          customer here, and a link that leads nowhere is worse than text. */}
      {record.parentOrganisationNumber && (
        <RegistryRow
          label={t("registryParentOrganisation")}
          value={<Text size="sm">{record.parentOrganisationNumber}</Text>}
        />
      )}
      {record.businessAddress && (
        <RegistryRow
          label={t("registryBusinessAddress")}
          value={<RegistryAddress address={record.businessAddress} />}
        />
      )}
      {record.postalAddress && (
        <RegistryRow label={t("registryPostalAddress")} value={<RegistryAddress address={record.postalAddress} />} />
      )}
    </Stack>
  );
};
