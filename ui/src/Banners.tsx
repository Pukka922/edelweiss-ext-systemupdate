import { useI18n } from "@edelweiss/sdk";
import { For, Show } from "solid-js";
import type { Banner, Status } from "./api";
import { formatDate } from "./format";

/**
 * The banners of the page, most urgent first: resume, restart (stale boot files first: the restart waits for them),
 * end of life, new release (or, in its place, that the next release has ended and there is none to offer).
 */
export function Banners(props: {
  status: Status;
  busy: boolean;
  rebootBlocked: boolean;
  onPlan: () => void;
  onResume: () => void;
  onReboot: () => void;
}) {
  const { t, locale } = useI18n();
  const date = (b: Banner) => (b.date ? formatDate(b.date, locale()) : "");
  const eol = () => props.status.banners.filter((b) => b.kind !== "new_release");
  const fresh = () => props.status.banners.filter((b) => b.kind === "new_release");
  return (
    <div class="flex flex-col gap-2">
      <Show when={props.status.resume}>
        {(r) => (
          <div role="alert" class="alert alert-error">
            <span>{t(`banner.resume.${r().reason}`, { target: r().target })}</span>
            <button type="button" class="btn btn-sm" disabled={props.busy} onClick={() => props.onResume()}>
              {t("banner.resumeButton")}
            </button>
          </div>
        )}
      </Show>
      <Show when={props.status.bootStale}>
        <div role="alert" class="alert alert-error">
          <span>{t("banner.bootStale")}</span>
        </div>
      </Show>
      <Show when={props.status.reboot}>
        <div class="alert alert-warning">
          <span>{t("banner.reboot")}</span>
          <button
            type="button"
            class="btn btn-sm"
            disabled={props.rebootBlocked}
            onClick={() => props.onReboot()}
          >
            {t("banner.rebootButton")}
          </button>
        </div>
      </Show>
      <For each={eol()}>
        {(b) => (
          <div
            class={
              b.kind.endsWith("_soon") || b.kind === "next_release_ended"
                ? "alert alert-warning"
                : "alert alert-error"
            }
          >
            <span>{t(`banner.${b.kind}`, { branch: b.branch, repo: b.repo ?? "", date: date(b) })}</span>
          </div>
        )}
      </For>
      <For each={fresh()}>
        {(b) => (
          <div class="alert alert-info">
            <Show
              when={props.status.mode === "sys"}
              fallback={<span>{t("banner.newReleaseDiskless", { branch: b.branch })}</span>}
            >
              <span>{t("banner.new_release", { branch: b.branch, date: date(b) })}</span>
              <button type="button" class="btn btn-sm" disabled={props.busy} onClick={() => props.onPlan()}>
                {t("banner.plan")}
              </button>
            </Show>
          </div>
        )}
      </For>
    </div>
  );
}
