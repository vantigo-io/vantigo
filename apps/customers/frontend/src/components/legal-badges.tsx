import { Anchor, Badge, type BadgeProps, Image, Text, Tooltip } from "@mantine/core";
import { useClipboard } from "@mantine/hooks";
import { type Icon, IconBuilding, IconUser } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import * as Flags from "country-flag-icons/react/3x2";
import type { ReactNode } from "react";

import { getLegalSource } from "../lib/legal-sources";
import "../i18n";

/** The icon shared by all badges of a given legal type. */
const legalTypeIcons: Record<string, Icon | undefined> = {
  business: IconBuilding,
  person: IconUser,
};

const capitalize = (value: string) => value.charAt(0).toUpperCase() + value.slice(1);

interface CopyableBadgeProps extends BadgeProps {
  /** Explanatory copy shown on hover/focus. */
  tooltip?: string;
  /** When set, clicking the badge copies this value to the clipboard. */
  copyValue?: string;
  children: ReactNode;
}

/**
 * A badge with an optional explanatory tooltip and click-to-copy behavior. When a
 * copy value is given, the badge becomes a button and the tooltip confirms the copy.
 */
export const CopyableBadge = ({ tooltip, copyValue, children, ...badgeProps }: CopyableBadgeProps) => {
  const { t } = useI18n("customers");
  const clipboard = useClipboard({ timeout: 1500 });

  const badge = copyValue ? (
    <Badge
      {...badgeProps}
      component="button"
      type="button"
      onClick={() => clipboard.copy(copyValue)}
      style={{ cursor: "pointer" }}
    >
      {children}
    </Badge>
  ) : (
    <Badge {...badgeProps}>{children}</Badge>
  );

  const label = clipboard.copied ? t("copied") : [tooltip, copyValue && t("clickToCopy")].filter(Boolean).join(" ");

  if (!label) {
    return badge;
  }

  return (
    <Tooltip label={label} maw={320} multiline events={{ hover: true, focus: true, touch: true }}>
      {badge}
    </Tooltip>
  );
};

interface LegalBadgeProps {
  /** The legal type ("business" or "person") deciding the prefix icon. */
  type: string;
  /** Explanatory copy shown on hover/focus. */
  tooltip?: string;
  /** When true, clicking the badge copies its value. */
  copyable?: boolean;
  children: string;
}

/** A neutral badge prefixed with the legal type's icon, used for legal names and ids. */
export const LegalValueBadge = ({ type, tooltip, copyable, children }: LegalBadgeProps) => {
  const TypeIcon = legalTypeIcons[type];

  return (
    <CopyableBadge
      variant="light"
      color="gray"
      radius="sm"
      tt="none"
      fw={500}
      leftSection={TypeIcon && <TypeIcon size={12} />}
      tooltip={tooltip}
      copyValue={copyable ? children : undefined}
    >
      {children}
    </CopyableBadge>
  );
};

/** A badge showing the legal type with its icon (building for business, user for person). */
export const LegalTypeBadge = ({ type, tooltip, copyable }: { type: string; tooltip?: string; copyable?: boolean }) => {
  const { t } = useI18n("customers");
  const TypeIcon = legalTypeIcons[type];

  return (
    <CopyableBadge
      variant="light"
      radius="sm"
      leftSection={TypeIcon && <TypeIcon size={12} />}
      tooltip={tooltip}
      copyValue={copyable ? type : undefined}
    >
      {type === "business" ? t("legalTypeBusiness") : type === "person" ? t("legalTypePerson") : capitalize(type)}
    </CopyableBadge>
  );
};

/** A badge showing the legal country with its flag. */
export const LegalCountryBadge = ({
  country,
  tooltip,
  copyable,
}: {
  country: string;
  tooltip?: string;
  copyable?: boolean;
}) => {
  const code = country.toUpperCase();
  const Flag = (Flags as Record<string, Flags.FlagComponent | undefined>)[code];

  return (
    <CopyableBadge
      variant="light"
      color="gray"
      radius="sm"
      leftSection={Flag && <Flag width={14} aria-hidden />}
      tooltip={tooltip}
      copyValue={copyable ? code : undefined}
    >
      {code}
    </CopyableBadge>
  );
};

/**
 * Shows where the legal identity data was retrieved from: the source's brand logo
 * (linked to its page for the entity when a legal id is given, otherwise its
 * website) when it has one, otherwise a badge with a fallback icon.
 */
export const LegalSourceBadge = ({
  source,
  tooltip,
  legalId,
}: {
  source: string;
  tooltip?: string;
  /** Deep-links the logo to the source's page for this entity. */
  legalId?: string;
}) => {
  const { t } = useI18n("customers");
  const info = getLegalSource(source);
  const label = info.labelKey ? t(info.labelKey) : info.label;
  const href = legalId && info.entityUrl ? info.entityUrl(legalId) : info.url;

  if (info.logo) {
    const logo = <Image src={info.logo} alt={label} h={14} w="auto" fit="contain" display="inline-block" />;

    return (
      <Tooltip label={tooltip ?? t("retrievedFrom", { source: label })} maw={320} multiline>
        {href ? (
          <Anchor href={href} target="_blank" rel="noreferrer" aria-label={label} lh={1}>
            {logo}
          </Anchor>
        ) : (
          logo
        )}
      </Tooltip>
    );
  }

  const FallbackIcon = info.icon;

  return (
    <CopyableBadge
      variant="light"
      color="gray"
      radius="sm"
      tt="none"
      fw={500}
      leftSection={FallbackIcon && <FallbackIcon size={12} />}
      tooltip={tooltip}
    >
      {label}
    </CopyableBadge>
  );
};

/** A dimmed placeholder for missing legal values. */
export const NoValue = () => (
  <Text component="span" size="sm" c="dimmed">
    —
  </Text>
);
