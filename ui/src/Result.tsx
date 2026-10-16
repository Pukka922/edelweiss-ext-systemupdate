import { useI18n } from "@edelweiss/sdk";
import { For, Show } from "solid-js";
import type { Job } from "./api";
import { fromV324 } from "./format";

const alertClass: Record<string, string> = {
  done: "alert-success",
  partial: "alert-warning",
  failed: "alert-error",
  failed_clean: "alert-warning",
  done_not_persisted: "alert-error",
  interrupted: "alert-error",
};

/** The grub-install command for the host, as far as it can be told (EFI: the vfat mount below /boot/efi). */
export function grubCommand(arch: string, efi: boolean): string {
  if (!efi) return "grub-install /dev/sdX";
  const target = arch === "aarch64" ? "arm64-efi" : arch === "x86" ? "i386-efi" : "x86_64-efi";
  return `grub-install --target=${target} --efi-directory=/boot/efi`;
}

export const releaseKind = (j: Job) => j.kind === "release" || j.kind === "resume";

/** A release upgrade that stopped halfway: no restart until it is resumed. */
export const partialRelease = (j: Job | undefined) => !!j && releaseKind(j) && j.status === "partial";

/**
 * This job changed the kernel, and the boot files were not fresh when it ended. What happened to that one job, shown
 * with its result for information: the restart gate of the page is the live bootStale of /status, which depends on no
 * job.
 */
export const staleBoot = (j: Job | undefined) => !!j?.post && j.post.kernelChanged && !j.post.bootOk;

/** A release upgrade to v3.24 or newer on a GRUB system: grub-install has to run before the restart. */
export const needsGrub = (j: Job | undefined) => !!j?.post?.grub && releaseKind(j) && fromV324(j.target);

/** Whether a restart after job j must wait: a partial release upgrade, stale boot files, or a missing grub-install. */
export function rebootHeld(j: Job | undefined, grubDone: boolean): boolean {
  return partialRelease(j) || staleBoot(j) || (needsGrub(j) && !grubDone);
}

/**
 * The result of a job, with its post-checks. reboot: the host needs a restart right now (from /status). rebootBlocked:
 * the page blocks the restart for a reason of its own (a job or apk runs, a release upgrade is not finished).
 */
export function Result(props: {
  job: Job;
  arch: string;
  reboot: boolean;
  rebootBlocked: boolean;
  grubDone: boolean;
  onGrubDone: (done: boolean) => void;
  onReboot: () => void;
}) {
  const { t } = useI18n();
  const j = () => props.job;
  const post = () => props.job.post;
  // a check that ended partial or failed (no network, a repository that did not answer) is no failed upgrade
  const message = () => {
    if (partialRelease(j())) return t("result.partialRelease");
    if (j().kind === "refresh" && j().status === "partial") return t("result.refreshPartial");
    if (j().kind === "refresh" && j().status === "failed") return t("result.refreshFailed");
    return t(`result.${j().status}`);
  };
  return (
    <section class="flex flex-col gap-3">
      <div role="alert" class={`alert ${alertClass[j().status] ?? "alert-info"} flex-col items-start`}>
        <span>{message()}</span>
        <Show when={j().error}>
          <span class="text-sm">{t("result.error", { message: j().error ?? "" })}</span>
        </Show>
        <Show when={j().apkAlive}>
          <span class="text-sm">{t("job.apkAlive", { pid: j().apkPid ?? "" })}</span>
        </Show>
      </div>
      <Show when={post()}>
        {(p) => (
          <>
            <Show when={staleBoot(j())}>
              <div class="alert alert-error">{t("result.dontReboot")}</div>
            </Show>
            <Show when={p().updateKernel}>
              <div class="alert alert-warning">{t("result.updateKernel")}</div>
            </Show>
            <Show when={props.reboot && p().reboot && !staleBoot(j()) && !partialRelease(j())}>
              <div class="alert alert-warning flex-col items-start gap-2">
                <span>{t("result.rebootNeeded")}</span>
                <Show when={needsGrub(j())}>
                  <span>{t("result.grub")}</span>
                  <code class="rounded bg-base-300 px-2 py-1">{grubCommand(props.arch, p().grubEfi)}</code>
                  <Show when={!p().grubEfi}>
                    <span class="text-sm">{t("result.grubBios")}</span>
                  </Show>
                  <label class="flex items-center gap-2">
                    <input
                      type="checkbox"
                      class="checkbox checkbox-sm"
                      checked={props.grubDone}
                      onChange={(e) => props.onGrubDone(e.currentTarget.checked)}
                    />
                    {t("result.grubDone")}
                  </label>
                </Show>
                <button
                  type="button"
                  class="btn btn-sm btn-warning"
                  disabled={props.rebootBlocked || rebootHeld(j(), props.grubDone)}
                  onClick={() => props.onReboot()}
                >
                  {t("banner.rebootButton")}
                </button>
              </div>
            </Show>
            <Show when={p().apkNew.length > 0}>
              <div>
                <p>{t("result.apkNew")}</p>
                <ul class="list-disc pl-4 font-mono text-sm">
                  <For each={p().apkNew}>{(f) => <li>{f}</li>}</For>
                </ul>
              </div>
            </Show>
            <Show when={p().stale.length > 0}>
              <div>
                <p>{t("result.stale")}</p>
                <ul class="list-disc pl-4 text-sm">
                  <For each={p().stale}>
                    {(s) => (
                      <li>
                        {s.service
                          ? t("result.staleService", { name: s.name, pid: s.pid, service: s.service })
                          : t("result.staleProcess", { name: s.name, pid: s.pid })}
                      </li>
                    )}
                  </For>
                </ul>
              </div>
            </Show>
            <Show when={p().removed.length > 0}>
              <div>
                <p>{t("result.removed")}</p>
                <ul class="list-disc pl-4 font-mono text-sm">
                  <For each={p().removed}>{(r) => <li>{r}</li>}</For>
                </ul>
              </div>
            </Show>
          </>
        )}
      </Show>
    </section>
  );
}
