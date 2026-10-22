// A stand-in for the Download dialog until the download branch (Track B)
// merges: the same props, so the desktop page's Download button is wired.
import type { Desktop } from "../api.ts";
import { Button, Dialog } from "../components/ui.tsx";

export function DownloadDialog({ desktop, onClose }: { desktop: Desktop; onClose: () => void }) {
  return (
    <Dialog
      title={`Download from ${desktop.name}`}
      intro="Downloading files isn't in this build yet."
      onClose={onClose}
      footer={
        <Button variant="primary" onClick={onClose}>
          Close
        </Button>
      }
    />
  );
}
