import { ConfirmDialog, createApiClient, useI18n, useSession, useToast } from "@edelweiss/sdk";
import { createSignal, onCleanup, Show } from "solid-js";
import { errorText } from "./errors";

/**
 * Restarts the host through the core (POST /api/system/reboot, admin mode) after a confirmation that names it, then
 * waits until /api/health fails once (the host went down) and answers again, and goes to the login.
 */
export function RebootDialog(props: {
  onClose: () => void;
  pollMs?: number;
  probeMs?: number;
  /** How long the host may keep answering before the page says so; default two minutes. */
  stillUpMs?: number;
  onBack?: () => void;
  onReload?: () => void;
}) {
  const session = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const core = createApiClient({
    base: "/api",
    csrfToken: () => session.csrfToken(),
    onUnauthorized: () => session.unauthorized(),
    refreshCsrf: () => session.refresh(),
  });
  const host = () => session.info()?.hostname ?? "";
  const [busy, setBusy] = createSignal(false);
  const [waiting, setWaiting] = createSignal(false);
  const [stillUp, setStillUp] = createSignal(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let alive = true;
  onCleanup(() => {
    alive = false;
    clearTimeout(timer);
  });
  const wait = (startedAt: number, wentDown: boolean) => {
    timer = setTimeout(async () => {
      let up = false;
      try {
        up = (
          await fetch("/api/health", {
            cache: "no-store",
            signal: AbortSignal.timeout(props.probeMs ?? 3000),
          })
        ).ok;
      } catch {
        up = false;
      }
      if (!alive) return;
      if (up && wentDown) {
        (props.onBack ?? (() => window.location.assign("/login")))();
        return;
      }
      if (up && Date.now() - startedAt > (props.stillUpMs ?? 120_000)) setStillUp(true);
      wait(startedAt, wentDown || !up);
    }, props.pollMs ?? 2000);
  };
  const confirm = async () => {
    setBusy(true);
    try {
      await core.post("/system/reboot", {});
      setWaiting(true);
      wait(Date.now(), false);
    } catch (err) {
      toast.error(t("reboot.failed", { message: errorText(err, t) }));
      props.onClose();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Show
      when={waiting()}
      fallback={
        <ConfirmDialog
          open
          title={t("reboot.title")}
          message={t("reboot.message", { name: host() })}
          confirmLabel={t("reboot.confirm")}
          danger
          busy={busy()}
          onConfirm={() => void confirm()}
          onCancel={() => props.onClose()}
        />
      }
    >
      <div
        class="fixed inset-0 z-50 flex flex-col items-center justify-center gap-4 bg-base-100"
        role="status"
      >
        <span class="loading loading-spinner loading-lg" aria-hidden="true" />
        <p class="text-lg">{t("reboot.waiting", { name: host() })}</p>
        <Show when={stillUp()}>
          <p class="text-sm text-base-content/70">{t("reboot.stillUp")}</p>
          <button
            type="button"
            class="btn btn-sm"
            onClick={() => (props.onReload ?? (() => window.location.reload()))()}
          >
            {t("reboot.reload")}
          </button>
        </Show>
      </div>
    </Show>
  );
}
