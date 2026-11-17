import { Alert, Badge, Button, type ButtonProps, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { fetchPdf } from "../api/pdf";
import {
  type PurchaseOrderStatus,
  type PurchasingVariantOption,
  purchasingSuppliersQueryOptions,
  searchPurchasingVariants,
} from "../api/purchasing";
import { isVersionConflict, PURCHASING_QUERY_KEY } from "../api/request";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";

const statusColor: Record<PurchaseOrderStatus, string> = {
  draft: "gray",
  sent: "blue",
  partially_received: "cyan",
  received: "green",
  closed: "dark",
  cancelled: "red",
};

/** An order's state, with Late and Unconfirmed beside it when they hold (P18, P19). */
export const OrderStatusBadges = ({
  status,
  late,
  unconfirmed,
}: {
  status: PurchaseOrderStatus;
  late?: boolean;
  unconfirmed?: boolean;
}) => {
  const { t } = usePurchasingFormat();
  return (
    <Group gap={4} wrap="nowrap">
      <Badge variant="light" color={statusColor[status]}>
        {t(`status.${status}`)}
      </Badge>
      {late && (
        <Badge variant="light" color="orange">
          {t("late")}
        </Badge>
      )}
      {unconfirmed && (
        <Badge variant="light" color="yellow">
          {t("unconfirmed")}
        </Badge>
      )}
    </Group>
  );
};

/**
 * A PDF behind a button: fetched first, so a refusal is a notification in
 * the reader's language, never the problem JSON in a tab.
 */
export const PdfButton = ({ url, children, ...button }: ButtonProps & { url: string; children: ReactNode }) => {
  const { t } = usePurchasingFormat();
  const [loading, setLoading] = useState(false);
  const download = async () => {
    setLoading(true);
    try {
      const pdf = await fetchPdf(url);
      const objectUrl = URL.createObjectURL(pdf.blob);
      const anchor = document.createElement("a");
      anchor.href = objectUrl;
      anchor.download = pdf.fileName ?? t("pdfFileName");
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotOpenPdf"), message: refusalMessage(error, t) });
    } finally {
      setLoading(false);
    }
  };
  return (
    <Button {...button} loading={loading} onClick={download}>
      {children}
    </Button>
  );
};

/** A yes-or-no question before an action, with the action's own words on the button. */
export const ConfirmDialog = ({
  title,
  body,
  confirm,
  color,
  loading,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  body?: string;
  confirm: string;
  color?: string;
  loading?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
}) => {
  const { t } = usePurchasingFormat();
  return (
    <Modal opened onClose={onClose} title={title}>
      <Stack>
        {body && <Text size="sm">{body}</Text>}
        {children}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color={color} loading={loading} onClick={onConfirm}>
            {confirm}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** A supplier of the register, searched by name, number or organisation number (P13). */
export const SupplierPicker = ({
  value,
  onChange,
  label,
  disabled,
  initialLabel,
  error,
}: {
  value: number | null;
  onChange: (supplierId: number | null) => void;
  label: string;
  disabled?: boolean;
  /** The picked supplier's words before a search has listed it. */
  initialLabel?: string;
  error?: string;
}) => {
  const { t } = usePurchasingFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const list = useQuery({ ...purchasingSuppliersQueryOptions(debounced.trim() || undefined), enabled: !disabled });
  const options = (list.data?.items ?? []).map((s) => ({
    value: String(s.id),
    label: t("supplierNumberName", { number: s.number, name: s.name }),
    disabled: !s.active,
  }));
  if (value !== null && initialLabel && !options.some((o) => o.value === String(value)))
    options.unshift({ value: String(value), label: initialLabel, disabled: false });
  return (
    <Select
      label={label}
      placeholder={t("pickSupplier")}
      searchable
      required
      disabled={disabled}
      data={options}
      value={value === null ? null : String(value)}
      onSearchChange={setSearch}
      filter={({ options: all }) => all}
      nothingFoundMessage={t("noSuppliersFound")}
      onChange={(next) => onChange(next === null ? null : Number(next))}
      error={error}
      data-assist="purchasing.new-order.supplier"
    />
  );
};

const variantLabel = (v: { sku: string; name: string }) => `${v.sku} · ${v.name}`;

/**
 * A variant searched by SKU, name or barcode through Purchasing's own search
 * (P16), with the supplier's item when the supplier sells it. Nothing is
 * listed until something is typed.
 */
export const VariantPicker = ({
  supplierId,
  onPick,
  label,
  size = "xs",
}: {
  supplierId?: number;
  onPick: (variant: PurchasingVariantOption) => void;
  label: string;
  size?: "xs" | "sm";
}) => {
  const { t } = usePurchasingFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search.trim(), 250);
  const found = useQuery({
    queryKey: [PURCHASING_QUERY_KEY, "variants", debounced, supplierId ?? null],
    queryFn: ({ signal }) => searchPurchasingVariants(debounced, supplierId, signal),
    enabled: debounced !== "",
  });
  const items = debounced === "" ? [] : (found.data?.items ?? []);
  return (
    <Select
      aria-label={label}
      placeholder={t("searchVariants")}
      searchable
      size={size}
      value={null}
      searchValue={search}
      onSearchChange={setSearch}
      data={items.map((v) => ({
        value: String(v.variantId),
        label: v.productStatus === "Discontinued" ? `${variantLabel(v)} (${t("discontinued")})` : variantLabel(v),
      }))}
      filter={({ options }) => options}
      nothingFoundMessage={debounced === "" || found.isFetching ? undefined : t("noVariantsFound")}
      onChange={(next) => {
        const picked = items.find((v) => String(v.variantId) === next);
        if (picked) {
          onPick(picked);
          setSearch("");
        }
      }}
    />
  );
};

/**
 * A save's refusal. A version conflict says somebody changed it meanwhile
 * and offers *Load again*, which reads Purchasing afresh — the form then
 * opens on what is stored now; any other refusal is said in words.
 */
export const SaveError = ({ error, conflict = "versionConflict" }: { error: unknown; conflict?: string }) => {
  const { t } = usePurchasingFormat();
  const queryClient = useQueryClient();
  if (isVersionConflict(error))
    return (
      <Alert color="yellow" title={t("couldNotSave")}>
        <Stack gap="xs" align="flex-start">
          <Text size="sm">{t(conflict)}</Text>
          <Button
            size="xs"
            variant="light"
            onClick={() => queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] })}
          >
            {t("reload")}
          </Button>
        </Stack>
      </Alert>
    );
  return (
    <Alert color="red" title={t("couldNotSave")}>
      {refusalMessage(error, t)}
    </Alert>
  );
};
