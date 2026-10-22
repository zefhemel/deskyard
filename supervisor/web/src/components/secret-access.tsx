// Asks before giving accounts access to desktops whose image contains
// secrets: desktops made from such an image start signed in as whoever made
// it, and the people given access can use those sign-ins.
import { Button, Dialog, LockBadge } from "./ui.tsx";
import { names } from "../lib/format.ts";

export interface SecretDesktop {
  /** The desktop's name. */
  name: string;
  /** The image's title (or ref). */
  image: string;
}

export function SecretAccessDialog({
  who,
  desktops,
  onCancel,
  onConfirm,
}: {
  who: string[];
  desktops: SecretDesktop[];
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const one = desktops.length === 1 ? desktops[0] : undefined;
  return (
    <Dialog
      title={`Give ${names(who)} access to ${names(desktops.map((d) => d.name))}?`}
      intro={
        <>
          {one ? (
            <>
              {one.name} runs on <b class="font-semibold">{one.image}</b> <LockBadge />, an image that contains secrets
            </>
          ) : (
            <>
              {desktops.map((d, i) => (
                <span key={d.name}>
                  {i > 0 && ", "}
                  {d.name} runs on <b class="font-semibold">{d.image}</b>
                </span>
              ))}{" "}
              <LockBadge />, images that contain secrets
            </>
          )}
          : desktops made from it start signed in as whoever made it. {names(who)} can use those sign-ins.
        </>
      }
      onClose={onCancel}
      onSubmit={onConfirm}
      footer={
        <>
          <Button variant="quiet" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" variant="danger">
            Give access
          </Button>
        </>
      }
    />
  );
}
