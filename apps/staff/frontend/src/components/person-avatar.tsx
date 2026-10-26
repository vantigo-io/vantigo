import { Avatar, type MantineSize, Menu, Text, UnstyledButton } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCamera, IconLock, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PROFILE_QUERY_KEY, useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, useRef } from "react";
import { removeMyAvatar, uploadMyAvatar } from "../api/account";
import { personPhotoUrl, removePersonPhoto, type StaffPerson, uploadPersonPhoto } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";

export interface PersonAvatarProps {
  person: Pick<StaffPerson, "id" | "displayName" | "photoUpdatedAt" | "photoSource" | "capabilities">;
  size?: MantineSize;
  /** Offers Change photo and Remove photo of HR's photo — when the person's capabilities allow them. */
  editable?: boolean;
  /**
   * The caller's own picture, on My page: Change photo and Remove photo set
   * their Vantigo avatar through identity, or Set by HR, which does nothing,
   * while HR's photo stands (photo sync design S2).
   */
  self?: boolean;
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
 * The person's picture, round, or their initials when there is none
 * (person-glance design S1, §6; photo sync design S1): HR's photo when set,
 * else the linked user's own Vantigo avatar (`photoSource`). For HR and
 * `staff:personal-data` (`canSetPhoto`) a menu on it sets HR's photo; for
 * the person on My page (`self`) the same menu sets their own avatar through
 * identity — and says Set by HR, an item that does nothing, while HR's photo
 * stands, which identity would refuse (409 `avatar_managed`). Change photo
 * takes a JPEG or PNG — a phone offers its camera and its library both —
 * scaled down first when it is large (scaledPhoto); Remove photo is there
 * when there is one to remove. The server judges the file; its refusal is
 * said as it words it, but for `avatar_managed`, said as Set by HR. On a
 * phone the avatar is a size smaller.
 */
export const PersonAvatar = ({ person, size = "lg", editable = false, self = false }: PersonAvatarProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)", undefined, { getInitialValueInEffect: false });
  const queryClient = useQueryClient();
  const picker = useRef<HTMLInputElement>(null);
  const shownSize = phone && size === "lg" ? "md" : size;
  const photo = person.photoUpdatedAt ? personPhotoUrl(person.id, person.photoUpdatedAt) : null;
  // The person's own avatar, not HR's photo: what My page offers them. `/me`
  // never grants canSetPhoto, so My page is always the person's own.
  const own = self;

  /**
   * The person's own avatar changed, or found managed: the person reads
   * again, and so does the shell's profile, whose avatar the header shows.
   */
  const ownChanged = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] }),
      queryClient.invalidateQueries({ queryKey: PROFILE_QUERY_KEY }),
    ]);

  /**
   * A refusal said as the server words it, but identity's 409
   * `avatar_managed` — HR's photo was set meanwhile — which is said as Set
   * by HR, after which the page reads again.
   */
  const refused = (error: Error, field?: string) => {
    if ((error as { code?: string }).code === "avatar_managed") {
      notifications.show({ color: "red", title: t("photoSetByHr"), message: t("photoAskHr") });
      void ownChanged();
      return;
    }
    notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, field) });
  };
  const change = useMutation({
    mutationFn: async (file: File) => {
      const scaled = await scaledPhoto(file);
      await (own ? uploadMyAvatar(scaled) : uploadPersonPhoto(person.id, scaled));
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("photoChanged"), message: "" });
      await (own ? ownChanged() : queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] }));
    },
    // Identity's refusal names no field; Staff's names `file`.
    onError: (error) => refused(error, own ? undefined : "file"),
  });
  const remove = useMutation({
    mutationFn: () => (own ? removeMyAvatar() : removePersonPhoto(person.id)),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("photoRemoved"), message: "" });
      await (own ? ownChanged() : queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] }));
    },
    onError: (error) => refused(error),
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
  if (!own && !(editable && person.capabilities.canSetPhoto)) return avatar;
  // While HR's photo stands the person's own choice is closed; the menu says why.
  const setByHr = own && person.photoSource === "record";
  // The person removes their own avatar alone, HR its photo alone — never the user's own avatar shown in its stead.
  const removable = own ? person.photoSource === "account" : photo !== null && person.photoSource !== "account";

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
            // "Photo of …", a menu: Change photo and Remove photo are its items, or Set by HR.
            aria-label={t("photoOf", { name: person.displayName })}
            style={{ borderRadius: "50%" }}
            disabled={change.isPending || remove.isPending}
          >
            {avatar}
          </UnstyledButton>
        </Menu.Target>
        <Menu.Dropdown>
          {setByHr ? (
            // Not `disabled`, which the menu's arrow keys skip and a screen reader
            // never reaches: focusable, aria-disabled, and it does nothing.
            <Menu.Item
              aria-disabled="true"
              closeMenuOnClick={false}
              style={{ cursor: "default" }}
              leftSection={<IconLock size={16} />}
            >
              <Text size="sm">{t("photoSetByHr")}</Text>
              <Text size="xs" c="dimmed">
                {t("photoAskHr")}
              </Text>
            </Menu.Item>
          ) : (
            <>
              <Menu.Item leftSection={<IconCamera size={16} />} onClick={() => picker.current?.click()}>
                {t("changePhoto")}
              </Menu.Item>
              {removable && (
                <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={confirmRemove}>
                  {t("removePhoto")}
                </Menu.Item>
              )}
            </>
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
