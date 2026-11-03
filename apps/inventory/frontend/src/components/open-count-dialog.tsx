import { Alert, Button, Group, Modal, Select, Stack, Text, Textarea } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { openCount } from "../api/counts";
import { sitesQueryOptions } from "../api/sites";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { countLinkOptions } from "../lib/routes";
import { LocationPicker } from "./location-picker";

export interface OpenCountDialogProps {
  opened: boolean;
  onClose: () => void;
  /** The site it opens on, as the site's or the location's page asks. */
  siteId?: number;
  /** The location whose subtree it counts, as the location's page asks; the whole site when absent. */
  locationId?: number;
}

/**
 * Opens a count (I16, I17): of a whole site, or of one location and
 * everything below it, with a note. Opening freezes what is expected on
 * every balance in scope; a scope that overlaps a count still counting is
 * refused (`count_scope_overlaps`). The count opens on its counting screen.
 */
export const OpenCountDialog = ({ opened, onClose, siteId: initialSite, locationId }: OpenCountDialogProps) => {
  const { t } = useInventoryFormat();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const invalidate = useInvalidateInventory();
  const sites = useQuery(sitesQueryOptions());
  const [siteId, setSiteId] = useState<number | undefined>(initialSite);
  const [scope, setScope] = useState<number | null>(locationId ?? null);
  const [note, setNote] = useState("");
  const [siteError, setSiteError] = useState<string>();
  const only = sites.data?.length === 1 ? sites.data[0].id : undefined;
  const site = siteId ?? only;

  const opening = useMutation({
    mutationFn: (chosen: number) =>
      openCount({
        siteId: chosen,
        ...(scope === null ? {} : { locationId: scope }),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async (count) => {
      await invalidate();
      onClose();
      await navigate(countLinkOptions(count.id));
    },
  });

  const submit = () => {
    if (site === undefined) {
      setSiteError(t("required"));
      return;
    }
    opening.mutate(site);
  };

  return (
    <Modal opened={opened} onClose={onClose} title={t("openCount")}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("openCountHint")}
        </Text>
        <Select
          label={t("site")}
          withAsterisk
          data={(sites.data ?? []).map((one) => ({ value: String(one.id), label: `${one.code} · ${one.name}` }))}
          value={site === undefined ? null : String(site)}
          onChange={(next) => {
            setSiteId(next === null ? undefined : Number(next));
            setScope(null);
            setSiteError(undefined);
          }}
          error={siteError}
        />
        <LocationPicker
          siteId={site}
          label={t("countScope")}
          placeholder={t("countWholeSite")}
          value={scope}
          onChange={setScope}
        />
        <Textarea
          label={t("note")}
          maxLength={1000}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        {opening.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotOpenCount")}>
            {refusalMessage(t, opening.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} loading={opening.isPending}>
            {t("openCount")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
