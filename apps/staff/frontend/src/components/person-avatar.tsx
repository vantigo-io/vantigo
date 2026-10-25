import { Avatar, type MantineSize, Menu, Text, UnstyledButton } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCamera, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, useRef } from "react";
import { personPhotoUrl, removePersonPhoto, type StaffPerson, uploadPersonPhoto } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";

export interface PersonAvatarProps {
  person: Pick<StaffPerson, "id" | "displayName" | "photoUpdatedAt" | "capabilities">;
  size?: MantineSize;
  /** Offers Change photo and Remove photo — when the person's capabilities allow them. */
  editable?: boolean;
}

/** The longer side, in pixels, and the size in bytes a photo is sent at most without being scaled down. */
const PHOTO_SIDE = 1024;
const PHOTO_BYTES = 1024 * 1024;

/**
 * The photo as it is sent: one larger than 1 MiB, or with a side over
 * 1024 px, drawn at most 1024 px on its longer side and exported as a JPEG
 * at quality 0.9 — a small one, a PNG included, is sent as it is. A browser
 * that cannot decode or draw it sends the original, which the server
 * judges either way.
 */
const scaledPhoto = async (file: File): Promise<File> => {
  if (typeof createImageBitmap !== "function" || typeof HTMLCanvasElement === "undefined") return file;
  let image: ImageBitmap;
  try {
    image = await createImageBitmap(file);
  } catch {
    return file;
  }
  try {
    const longer = Math.max(image.width, image.height);
    if (file.size <= PHOTO_BYTES && longer <= PHOTO_SIDE) return file;
    const scale = Math.min(1, PHOTO_SIDE / longer);
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(image.width * scale));
    canvas.height = Math.max(1, Math.round(image.height * scale));
    const context = canvas.getContext("2d");
    if (!context) return file;
    // A JPEG has no transparency: a PNG's clear pixels become white, not black.
    context.fillStyle = "#fff";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.9));
    if (!blob) return file;
    return new File([blob], `${file.name.replace(/\.[^.]*$/, "")}.jpg`, { type: "image/jpeg" });
  } finally {
    image.close();
  }
};

/**
 * The person's photo, round, or their initials when there is none
 * (person-glance design S1, §6). For whoever may set it — the person, HR and
 * `staff:personal-data` (`canSetPhoto`) — a menu on it: Change photo, a JPEG
 * or PNG — a phone offers its camera and its library both — scaled down
 * first when it is large (scaledPhoto), and Remove photo when there is one.
 * The server judges the file; its refusal is said as it words it. On a phone
 * the avatar is a size smaller.
 */
export const PersonAvatar = ({ person, size = "lg", editable = false }: PersonAvatarProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)", undefined, { getInitialValueInEffect: false });
  const queryClient = useQueryClient();
  const picker = useRef<HTMLInputElement>(null);
  const shownSize = phone && size === "lg" ? "md" : size;
  const photo = person.photoUpdatedAt ? personPhotoUrl(person.id, person.photoUpdatedAt) : null;

  const change = useMutation({
    mutationFn: async (file: File) => uploadPersonPhoto(person.id, await scaledPhoto(file)),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("photoChanged"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, "file") }),
  });
  const remove = useMutation({
    mutationFn: () => removePersonPhoto(person.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("photoRemoved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error) }),
  });

  const avatar = (
    <Avatar
      src={photo}
      alt={person.displayName}
      name={person.displayName}
      color="initials"
      radius="xl"
      size={shownSize}
      aria-label={person.displayName}
      // The initials Mantine draws from the name stand for it to a screen reader too.
      {...(photo ? {} : { role: "img" })}
    />
  );
  if (!editable || !person.capabilities.canSetPhoto) return avatar;

  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    // Cleared, so picking the same file again is a pick too.
    event.currentTarget.value = "";
    if (file) change.mutate(file);
  };
  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("removePhoto"),
      children: <Text size="sm">{t("removePhotoConfirm")}</Text>,
      labels: { confirm: t("remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  return (
    <>
      <Menu position="bottom-start" withArrow>
        <Menu.Target>
          <UnstyledButton
            // "Photo of …", a menu: Change photo and Remove photo are its items.
            aria-label={t("photoOf", { name: person.displayName })}
            style={{ borderRadius: "50%" }}
            disabled={change.isPending || remove.isPending}
          >
            {avatar}
          </UnstyledButton>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<IconCamera size={16} />} onClick={() => picker.current?.click()}>
            {t("changePhoto")}
          </Menu.Item>
          {photo && (
            <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={confirmRemove}>
              {t("removePhoto")}
            </Menu.Item>
          )}
        </Menu.Dropdown>
      </Menu>
      {/* Outside the menu, which unmounts when it closes: the picker must outlive it. */}
      <input
        ref={picker}
        type="file"
        accept="image/jpeg,image/png"
        aria-label={t("chooseFile")}
        style={{ display: "none" }}
        onChange={pick}
      />
    </>
  );
};
