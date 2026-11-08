import { Avatar, type AvatarProps, Badge, Indicator, type MantineSize } from "@mantine/core";
import { registerCatalog, useI18n } from "../i18n";
import { shellCatalog } from "../i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

/** What a user is: a person who signs in, or an AI agent (the Agents app). */
export type UserKind = "person" | "agent";

/** The small "AI" label an agent's avatar carries, so nobody mistakes it for a person. */
export const AiBadge = () => {
  const { t } = useI18n("shell");
  return (
    <Badge size="xs" variant="filled" color="grape" radius="sm" px={3} data-testid="ai-badge">
      {t("aiBadge")}
    </Badge>
  );
};

export interface UserAvatarProps extends Omit<AvatarProps, "name"> {
  /** The user's display name: its initials show when there is no picture. */
  name?: string | null;
  /** The picture's URL, when the user has one. */
  src?: string | null;
  /** `agent` puts the AI badge on the avatar; anything else is a plain avatar. */
  kind?: UserKind | null;
  size?: MantineSize | number;
  "aria-label"?: string;
}

/**
 * A user's avatar, with the AI badge on it when the user is an agent. The
 * badge is part of the label a screen reader hears, too.
 */
export const UserAvatar = ({ name, src, kind, size = "md", "aria-label": ariaLabel, ...rest }: UserAvatarProps) => {
  const { t } = useI18n("shell");
  const isAgent = kind === "agent";
  const avatar = (
    <Avatar
      src={src ?? undefined}
      name={name || undefined}
      color="initials"
      radius="xl"
      size={size}
      {...rest}
      aria-label={isAgent && name ? t("aiAvatarLabel", { name }) : ariaLabel}
    />
  );
  if (!isAgent) return avatar;
  return (
    <Indicator label={<AiBadge />} position="bottom-end" size={16} offset={2} color="transparent" inline>
      {avatar}
    </Indicator>
  );
};
