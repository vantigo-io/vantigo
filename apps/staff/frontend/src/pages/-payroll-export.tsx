import { Button, Group, Text } from "@mantine/core";
import { MonthPickerInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconDownload } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { exportPayroll } from "../api/data";
import { saveFile } from "../api/documents";
import type { ApiError } from "../api/request";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { fromUtc, toUtc } from "../lib/dates";

/** The first day of the month before `today`'s (both `YYYY-MM-DD`), in UTC as every Staff date is. */
const previousMonth = (today: string): string => {
  const day = toUtc(`${today.slice(0, 7)}-01`);
  day.setUTCMonth(day.getUTCMonth() - 1);
  return fromUtc(day);
};

/**
 * The payroll export (design S18), `staff:manage`'s with `staff:personal-data` (its leave rows name HR-only leave): a month — the one
 * before the business zone's today unless another is picked, payroll being
 * run for the month just closed — sent as `YYYY-MM` and saved under the
 * server's name, `staff-payroll-YYYY-MM.csv`. The file carries no sealed
 * value; the three 2025 appointment forms' codes are unverified against
 * Skatteetaten's list, which the card says beneath the button.
 */
export const PayrollExportCard = ({ today }: { today: string }) => {
  const { t } = useI18n("staff");
  const [month, setMonth] = useState<string | null>(previousMonth(today));
  const exporting = useMutation({
    mutationFn: (chosen: string) => exportPayroll(chosen),
    onSuccess: saveFile,
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("couldNotExportPayroll"),
        message: (error as ApiError).code === "payroll_month_invalid" ? t("payrollMonthInvalid") : error.message,
      }),
  });

  return (
    <FieldGroup title={t("payrollTitle")} description={t("payrollDescription")}>
      <Group align="flex-end" gap="sm">
        <MonthPickerInput label={t("payrollMonth")} value={month} onChange={setMonth} allowDeselect={false} w={220} />
        <Button
          leftSection={<IconDownload size={16} />}
          disabled={!month}
          loading={exporting.isPending}
          onClick={() => month && exporting.mutate(month.slice(0, 7))}
        >
          {t("exportForPayroll")}
        </Button>
      </Group>
      <Text size="xs" c="dimmed">
        {t("payrollCaveat")}
      </Text>
    </FieldGroup>
  );
};
