import { useI18n } from "@vantigo/frontend-shell";
import type { CustomerOwner } from "../api/customers";
import "../i18n";
import { UserPicker } from "./user-picker";

/**
 * Who owns a customer relationship (owner and tags design D1, D3). It is
 * `UserPicker` with this field's own words — the picker itself was generalised
 * when follow-ups needed the same control for an assignee (follow-ups design
 * D4), and every rule it followed still applies here unchanged; see its doc
 * comment. This wrapper stays rather than the Relationship card calling
 * `UserPicker` directly, so the owner's four strings live in one place instead
 * of at the call site.
 */
export const OwnerPicker = ({
  value,
  selected,
  onChange,
  disabled,
}: {
  value: string | null;
  /** The owner the customer already has, so a name survives a search that does not contain it. */
  selected?: CustomerOwner | null;
  onChange: (value: string | null) => void;
  disabled?: boolean;
}) => {
  const { t } = useI18n("customers");
  return (
    <UserPicker
      label={t("owner")}
      placeholder={t("searchOwners")}
      clearLabel={t("clearOwner")}
      value={value}
      selected={selected}
      onChange={onChange}
      disabled={disabled}
    />
  );
};
