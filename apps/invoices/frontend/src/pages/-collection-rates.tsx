import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import dayjs from "dayjs";
import { useState } from "react";
import {
  addCollectionRate,
  type CollectionRate,
  type CollectionRateKind,
  collectionRatesQueryOptions,
  deleteCollectionRate,
  halfYearly,
  RATE_KINDS,
} from "../api/rates";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { useWho } from "../lib/bank";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface CollectionRatesCardProps {
  /** Today in Oslo, meta's: a new rate takes effect after it. */
  today: string;
  currentUserId?: string;
}

/** Whether a row may be deleted: a manager's own, not yet in force, and no letter has used it (D6). */
const deletable = (rate: CollectionRate, today: string) =>
  !rate.seeded && rate.usable && !rate.inForce && rate.validFrom > today;

/**
 * The collection rates (invoices payments and reminders design D6): the late
 * interest rate, the business compensation and the inkassosats as dated rows,
 * by kind, the row in force today highlighted, who added a row a release did
 * not, and the release's own value where it differs from a manager's — with
 * the list's warning. A manager adds a row that takes effect after today and
 * deletes one of their own not yet in force that no letter used; a seeded row
 * is never deleted. Each refusal in words.
 */
export const CollectionRatesCard = ({ today, currentUserId }: CollectionRatesCardProps) => {
  const { t, date, money, number } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const queryClient = useQueryClient();
  const rates = useQuery(collectionRatesQueryOptions());
  const [adding, setAdding] = useState(false);
  const remove = useMutation({
    mutationFn: (rate: CollectionRate) => deleteCollectionRate(rate.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("rates.deleted") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("rates.couldNotDelete"), message: refusalMessage(error, t, date) }),
  });
  const rateText = (kind: CollectionRateKind, value: number) =>
    kind === "late_interest_percent" ? t("rates.percent", { value: number(value, 2) }) : money(value, "NOK");
  const confirmDelete = (rate: CollectionRate) =>
    modals.openConfirmModal({
      title: t("rates.deleteTitle"),
      children: (
        <Text size="sm">
          {t("rates.deleteBody", { kind: t(`rates.kind.${rate.kind}`), date: date(rate.validFrom) })}
        </Text>
      ),
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(rate),
    });
  return (
    <Card withBorder data-testid="collection-rates">
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("rates.title")}</Title>
          <Button leftSection={<IconPlus size={16} />} variant="light" onClick={() => setAdding(true)}>
            {t("rates.add")}
          </Button>
        </Group>
        <Text size="sm" c="dimmed">
          {t("rates.description")}
        </Text>
        {rates.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("rates.couldNotLoad")}>
            {refusalMessage(rates.error, t, date)}
          </Alert>
        )}
        {rates.isPending && <ContentSkeleton rows={4} rowHeight={36} />}
        {rates.data?.warnings.includes("collection_rate_differs_from_release") && (
          <Alert color="yellow" icon={<IconAlertCircle size={16} />} data-testid="rates-differ">
            {t("rates.differsFromRelease")}
          </Alert>
        )}
        {rates.data &&
          RATE_KINDS.map((kind) => {
            const rows = rates.data.rates
              .filter((r) => r.kind === kind)
              .sort((a, b) => a.validFrom.localeCompare(b.validFrom));
            return (
              <Stack key={kind} gap={4}>
                <Title order={5}>{t(`rates.kind.${kind}`)}</Title>
                <Table aria-label={t(`rates.kind.${kind}`)}>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("rates.col.validFrom")}</Table.Th>
                      <Table.Th ta="right">{t("rates.col.value")}</Table.Th>
                      <Table.Th>{t("rates.col.source")}</Table.Th>
                      <Table.Th>{t("rates.col.origin")}</Table.Th>
                      <Table.Th />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {rows.map((rate) => (
                      <Table.Tr
                        key={rate.id}
                        data-rate={rate.id}
                        data-in-force={rate.inForce ? "true" : undefined}
                        bg={rate.inForce ? "var(--mantine-color-blue-light)" : undefined}
                      >
                        <Table.Td>
                          {date(rate.validFrom)}{" "}
                          {rate.inForce && (
                            <Badge size="sm" variant="filled">
                              {t("rates.inForce")}
                            </Badge>
                          )}
                        </Table.Td>
                        <Table.Td ta="right">
                          {rateText(rate.kind, rate.value)}
                          {rate.releaseValue !== undefined && rate.releaseValue !== rate.value && (
                            <Text size="xs" c="orange" data-testid={`release-differs-${rate.id}`}>
                              {t("rates.releaseValue", {
                                value: rateText(rate.kind, rate.releaseValue),
                                source: rate.releaseSourceRef ?? "",
                              })}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>{rate.sourceRef}</Table.Td>
                        <Table.Td>
                          {rate.seeded ? t("rates.seeded") : t("rates.addedBy", { who: who(rate.createdBy) })}
                        </Table.Td>
                        <Table.Td ta="right">
                          {deletable(rate, today) && (
                            <ActionIcon
                              color="red"
                              variant="subtle"
                              aria-label={t("rates.deleteOf", {
                                kind: t(`rates.kind.${kind}`),
                                date: date(rate.validFrom),
                              })}
                              loading={remove.isPending && remove.variables?.id === rate.id}
                              onClick={() => confirmDelete(rate)}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Stack>
            );
          })}
      </Stack>
      {adding && <AddRateModal today={today} onClose={() => setAdding(false)} />}
    </Card>
  );
};

const rateInputs = new Set(["kind", "validFrom", "value", "sourceRef"]);

/** A new rate ahead of a release: its kind, the day it takes effect, its value within the kind's bounds and its regulation. */
const AddRateModal = ({ today, onClose }: { today: string; onClose: () => void }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [kind, setKind] = useState<CollectionRateKind>("late_interest_percent");
  const [validFrom, setValidFrom] = useState<string | null>(null);
  const [value, setValue] = useState<number | string>("");
  const [sourceRef, setSourceRef] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const clear = (field: string) =>
    setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== field)));
  const add = useMutation({
    mutationFn: () =>
      addCollectionRate({ kind, validFrom: validFrom ?? "", value: Number(value), sourceRef: sourceRef.trim() }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("rates.added") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => rateInputs.has(field), "rate");
        setErrors(onInputs);
        if (elsewhere.length > 0)
          notifications.show({ color: "red", title: t("rates.couldNotAdd"), message: elsewhere.join(" ") });
        return;
      }
      notifications.show({ color: "red", title: t("rates.couldNotAdd"), message: refusalMessage(error, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("rates.add")}>
      <Stack>
        <Select
          label={t("rates.col.kind")}
          allowDeselect={false}
          data={RATE_KINDS.map((k) => ({ value: k, label: t(`rates.kind.${k}`) }))}
          value={kind}
          error={errors.kind}
          onChange={(v) => {
            if (v) setKind(v as CollectionRateKind);
            clear("kind");
          }}
        />
        <DateInput
          label={t("rates.col.validFrom")}
          description={halfYearly(kind) ? t("rates.validFromHalfYear") : t("rates.validFromHint")}
          required
          valueFormat={t("dateInputFormat")}
          // The server takes a day after today only.
          minDate={dayjs(today).add(1, "day").format("YYYY-MM-DD")}
          value={validFrom}
          error={errors.validFrom}
          onChange={(v) => {
            setValidFrom(v);
            clear("validFrom");
          }}
        />
        <NumberInput
          label={t("rates.col.value")}
          description={t(`rates.bounds.${kind}`)}
          required
          decimalScale={2}
          hideControls
          value={value}
          error={errors.value}
          onChange={(v) => {
            setValue(v);
            clear("value");
          }}
        />
        <TextInput
          label={t("rates.col.source")}
          description={t("rates.sourceHint")}
          required
          maxLength={100}
          value={sourceRef}
          error={errors.sourceRef}
          onChange={(e) => {
            setSourceRef(e.currentTarget.value);
            clear("sourceRef");
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={add.isPending || !validFrom || value === "" || sourceRef.trim() === ""}
            loading={add.isPending}
            onClick={() => add.mutate()}
          >
            {t("rates.add")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
