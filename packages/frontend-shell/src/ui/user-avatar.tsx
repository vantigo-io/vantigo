import { Avatar, type AvatarProps, Badge, Indicator, type MantineSize } from "@mantine/core";
import { registerCatalog, useI18n } from "../i18n";
import { shellCatalog } from "../i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

/**
 * What a user is: a person who signs in, an AI agent (the Agents app), or an
 * integration — an external application's account that calls the API with a
 * key (Workspace admin › API access).
 */
export type UserKind = "person" | "agent" | "integration";

/** The small "AI" label an agent's avatar carries, so nobody mistakes it for a person. */
export const AiBadge = () => {
  const { t } = useI18n("shell");
  return (
    <Badge size="xs" variant="filled" color="grape" radius="sm" px={3} data-testid="ai-badge">
      {t("aiBadge")}
    </Badge>
  );
};

/** The small "API" label an integration's avatar carries: it is an application, not a person. */
export const ApiBadge = () => {
  const { t } = useI18n("shell");
  return (
    <Badge size="xs" variant="filled" color="gray" radius="sm" px={3} data-testid="api-badge">
      {t("apiBadge")}
    </Badge>
  );
};

export interface UserAvatarProps extends Omit<AvatarProps, "name"> {
  /** The user's display name: its initials show when there is no picture. */
  name?: string | null;
  /** The picture's URL, when the user has one. */
  src?: string | null;
  /** `agent` puts the AI badge on the avatar, `integration` the API badge; anything else is a plain avatar. */
  kind?: UserKind | null;
  size?: MantineSize | number;
  "aria-label"?: string;
}

/**
 * A user's avatar, with the AI badge on it when the user is an agent and the
 * API badge when it is an integration. The badge is part of the label a screen
 * reader hears, too.
 */
export const UserAvatar = ({ name, src, kind, size = "md", "aria-label": ariaLabel, ...rest }: UserAvatarProps) => {
  const { t } = useI18n("shell");
  const isAgent = kind === "agent";
  const isIntegration = kind === "integration";
  const avatar = (
    <Avatar
      src={src ?? undefined}
      name={name || undefined}
      color="initials"
      radius="xl"
      size={size}
      {...rest}
      aria-label={
        isAgent && name
          ? t("aiAvatarLabel", { name })
          : isIntegration && name
            ? t("apiAvatarLabel", { name })
            : ariaLabel
      }
    />
  );
  if (!isAgent && !isIntegration) return avatar;
  return (
    <Indicator
      label={isAgent ? <AiBadge /> : <ApiBadge />}
      position="bottom-end"
      size={16}
      offset={2}
      color="transparent"
      inline
    >
      {avatar}
    </Indicator>
  );
};
