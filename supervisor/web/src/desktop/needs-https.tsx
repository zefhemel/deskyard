// One line for a feature the browser turns off over plain http.
import { httpsHelp } from "./selkies.ts";

export function NeedsHttps() {
  return (
    <p data-needs-https class="mt-1.5 text-[12.5px] text-muted">
      Needs https, see{" "}
      <a href={httpsHelp} target="_blank" rel="noreferrer" class="text-accent underline">
        running on a LAN
      </a>
    </p>
  );
}
