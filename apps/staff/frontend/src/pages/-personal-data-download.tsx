import { Button, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconDownload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { saveFile } from "../api/documents";
import { downloadPersonalData, type StaffPerson } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";

/**
 * The complete personal-data file (design S16) — HR's answer to an innsyn
 * request, `staff:personal-data`'s (`canHold`). It opens the sealed values,
 * so it is an audited reveal: the page says so and asks first, as a reveal
 * does, and the history it records the export in is read again after.
 */
export const PersonalDataDownload = ({ person }: { person: StaffPerson }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const download = useMutation({
    mutationFn: () => downloadPersonalData(person),
    onSuccess: async (file) => {
      saveFile(file);
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "people", "detail", person.id, "events"] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDownloadPersonalData"), message: error.message }),
  });
  const confirm = () =>
    modals.openConfirmModal({
      title: t("downloadPersonalDataTitle", { name: person.displayName }),
      children: <Text size="sm">{t("downloadPersonalDataExplained")}</Text>,
      labels: { confirm: t("download"), cancel: t("cancel") },
      onConfirm: () => download.mutate(),
    });
  return (
    <Button variant="light" leftSection={<IconDownload size={16} />} loading={download.isPending} onClick={confirm}>
      {t("downloadPersonalData")}
    </Button>
  );
};
