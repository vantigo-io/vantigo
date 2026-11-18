import { MultiSelect } from "@mantine/core";
import { type AuthorizationRole, isCustomRole } from "../api/authorization";

/**
 * The custom-role picker of Roles & access, shared with API access (API
 * access design K22): only custom roles are offered, so Owner, SystemAdmin
 * and User are never a choice, whatever the roles given.
 */
export const CustomRolePicker = ({
  label,
  description,
  roles,
  value,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  roles: readonly AuthorizationRole[];
  value: string[];
  onChange: (roleIds: string[]) => void;
  disabled?: boolean;
}) => (
  <MultiSelect
    label={label}
    description={description}
    data={roles.filter(isCustomRole).map((role) => ({ value: role.id, label: role.displayName }))}
    value={value}
    onChange={onChange}
    disabled={disabled}
  />
);
