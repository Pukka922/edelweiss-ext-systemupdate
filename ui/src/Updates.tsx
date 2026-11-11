import {
  ConfirmDialog,
  ErrorAlert,
  LoadingState,
  PageHeader,
  useApi,
  useI18n,
  useSession,
  useToast,
} from "@edelweiss/sdk";
import {
  createEffect,
  createMemo,
  createResource,
  createSignal,
  Match,
  onCleanup,
  Show,
  Switch,
} from "solid-js";
import type { Job, Plan, Releases, Status, Updates as UpdateData } from "./api";
import { Banners } from "./Banners";
import { errorText } from "./errors";
import { formatAgo, formatBytes } from "./format";
import { History } from "./History";
import { JobPanel } from "./JobPanel";
import { RebootDialog } from "./RebootDialog";
import { ReleaseDialog } from "./ReleaseDialog";
import { needsGrub, partialRelease, Result, releaseKind } from "./Result";
import { UpdateList } from "./UpdateList";
import { UpgradeDialog } from "./UpgradeDialog";

const AUTO_CHECK_MS = 60 * 60 * 1000; // decision 5a: check when the page opens, if the last check is older
const APK_POLL_MS = 5000; // how often the page asks again while apk of a dead runner still runs

type Dialog = "upgrade" | "release" | "resume" | "reboot";

export function Updates(props: { apkPollMs?: number }) {
  const api = useApi();
  const session = useSession();
  const i18n = useI18n();
  const { t } = i18n;
  const toast = useToast();
  // session.admin() reads the whole session object, which the shell replaces on every focus refresh
  const admin = createMemo(() => session.admin());
  // a user who cannot become admin cannot use any of the actions: requestAdmin would resolve false
  const canAdmin = createMemo(() => session.canAdmin());
  const key = () => ({ admin: admin() }); // always truthy: the resources load in either mode
  const [status, { refetch: refetchStatus }] = createResource(key, () => api.get<Status>("/status"));
  const [updates, { refetch: refetchUpdates }] = createResource(key, () => api.get<UpdateData>("/updates"));
  const [releases, { refetch: refetchReleases }] = createResource(key, () => api.get<Releases>("/releases"));
  const [history, { refetch: refetchHistory }] = createResource(key, () => api.get<{ jobs: Job[] }>("/jobs"));
  // reading an errored resource throws: these check the error first
  const st = () => (status.error ? undefined : status());
  const ups = () => (updates.error ? undefined : updates());
  const rels = () => (releases.error ? undefined : releases());
  const [running, setRunning] = createSignal<Job>();
  const [dialog, setDialog] = createSignal<Dialog>();
  const [grubDone, setGrubDone] = createSignal(false);
  const [lastPlan, setLastPlan] = createSignal<Plan>();
  const [resuming, setResuming] = createSignal(false);

  // follow the job the status reports (after a reload, in another tab, after admin mode changed); but not the job that
  // just ended here, which the status still reports as current until its reload has answered
  let endedId: string | undefined;
  createEffect(() => {
    const cur = st()?.current;
    if (cur && cur.id !== endedId && !running()) setRunning(cur);
  });

  const reload = () => {
    void refetchStatus();
    void refetchUpdates();
    void refetchReleases();
    void refetchHistory();
  };
  // the size of the last preview says nothing once a job has run (or runs): the list it was made for has changed
  const started = (job: Job) => {
    setDialog(undefined);
    setLastPlan(undefined);
    setRunning(job);
    void refetchHistory();
  };
  const ended = (job: Job) => {
    endedId = job.id;
    setRunning(undefined);
    setLastPlan(undefined);
    setGrubDone(false);
    reload();
  };
  /** Runs f in admin mode: asks for it first when it is off. */
  const act = async (f: () => unknown) => {
    if (!session.admin() && !(await session.requestAdmin())) return;
    await f();
  };
  // the automatic check of the visit: once, and not after a manual check, which asks for admin mode first when it is off
  // and would otherwise find the automatic check started in the same moment, whose refresh the server answers with 409
  let autoChecked = false;
  const refresh = () => {
    autoChecked = true;
    return act(async () => {
      try {
        started(await api.post<Job>("/refresh"));
      } catch (err) {
        toast.error(errorText(err, t));
      }
    });
  };
  const resume = async () => {
    setResuming(true);
    try {
      started(await api.post<Job>("/jobs", { kind: "resume" }));
    } catch (err) {
      setDialog(undefined);
      toast.error(errorText(err, t));
    } finally {
      setResuming(false);
    }
  };

  // a job whose runner died while its apk still runs is no "current" job, but apk holds the lock all the same; nothing
  // reports its end to the page, so the page asks again until it has finished
  const apkAlive = createMemo(() => !!st()?.last?.apkAlive);
  createEffect(() => {
    if (!apkAlive()) return;
    const timer = setInterval(() => {
      void refetchStatus();
      void refetchHistory();
    }, props.apkPollMs ?? APK_POLL_MS);
    onCleanup(() => clearInterval(timer));
  });

  createEffect(() => {
    const s = st();
    if (!s || autoChecked || !admin() || s.current || apkAlive() || running()) return;
    autoChecked = true;
    const age = s.checked ? Date.now() - Date.parse(s.checked) : Number.POSITIVE_INFINITY;
    if (age > AUTO_CHECK_MS) void refresh();
  });

  // status.last is the newest finished job of any kind, so a later check hides what an earlier release upgrade asks
  // for: the page looks for the newest release or resume job in the history, which carries its post-checks
  const jobs = () => (history.error ? undefined : history()?.jobs);
  const releaseJob = () => jobs()?.find(releaseKind);
  // the release job whose grub-install the restart waits for: its result takes the place of the result of the last job
  const grubJob = () => {
    const r = releaseJob();
    return st()?.reboot && needsGrub(r) ? r : undefined;
  };
  const resultJob = () => {
    const last = st()?.last;
    return grubJob() ?? (last && (last.kind !== "refresh" || last.status !== "done") ? last : undefined);
  };

  const busy = () => !!running() || !!st()?.current || apkAlive();
  // an unknown history is no free pass: a release upgrade in it may need grub-install or a resume. Stale boot files are
  // the live check of the status, which depends on no job: a later check or install must not forget a failed initramfs,
  // and a kernel an earlier job installed must not block the restart when its boot files are fine.
  const rebootBlocked = () =>
    busy() ||
    !!st()?.resume ||
    !jobs() ||
    partialRelease(releaseJob()) ||
    (needsGrub(releaseJob()) && !grubDone()) ||
    !!st()?.bootStale;
  // what the buttons check: a job or apk running, or no way to admin mode (their click would do nothing)
  const actionsOff = () => busy() || !canAdmin();
  const restartOff = () => rebootBlocked() || !canAdmin();
  const securityCount = () => ups()?.items.filter((u) => u.security.length > 0).length ?? 0;

  return (
    <div class="flex flex-col gap-6">
      <Switch fallback={<LoadingState label={t("loading")} />}>
        <Match when={status.error}>
          <ErrorAlert error={status.error} onRetry={() => void refetchStatus()} />
        </Match>
        <Match when={st()}>
          {(s) => (
            <>
              <PageHeader
                title={t("title")}
                subtitle={t("subtitle", {
                  release: s().release,
                  arch: s().arch,
                  mode: t(`mode.${s().mode}`),
                })}
                actions={
                  <>
                    <span class="self-center text-sm text-base-content/70">
                      {s().checked
                        ? t("checked.ago", { ago: formatAgo(s().checked ?? "", Date.now(), i18n.locale()) })
                        : t("checked.never")}
                    </span>
                    <button
                      type="button"
                      class="btn btn-sm"
                      disabled={actionsOff()}
                      onClick={() => void refresh()}
                    >
                      {t("check")}
                    </button>
                  </>
                }
              />
              <Show when={!canAdmin()}>
                <p class="text-sm text-base-content/70">{t("adminHint")}</p>
              </Show>
              <Banners
                status={s()}
                busy={actionsOff()}
                rebootBlocked={restartOff()}
                onPlan={() => void act(() => setDialog("release"))}
                onResume={() => void act(() => setDialog("resume"))}
                onReboot={() => void act(() => setDialog("reboot"))}
              />
              <Show
                when={running()}
                fallback={
                  <Show when={resultJob()}>
                    {(l) => (
                      <Result
                        job={l()}
                        arch={s().arch}
                        reboot={s().reboot}
                        grubDone={grubDone()}
                        onGrubDone={setGrubDone}
                      />
                    )}
                  </Show>
                }
              >
                {(j) => <JobPanel job={j()} onEnd={ended} />}
              </Show>
              <Show when={ups()}>
                {(u) => (
                  <>
                    <div class="stats stats-vertical bg-base-200 sm:stats-horizontal">
                      <div class="stat">
                        <div class="stat-title">{t("stats.available")}</div>
                        <div class="stat-value">{u().items.length}</div>
                      </div>
                      <Show when={u().securityData}>
                        <div class="stat">
                          <div class="stat-title">{t("stats.security")}</div>
                          <div class="stat-value text-error">{securityCount()}</div>
                        </div>
                      </Show>
                      <Show when={lastPlan()?.simulation.sizesKnown && lastPlan()}>
                        {(p) => (
                          <div class="stat">
                            <div class="stat-title">{t("stats.download")}</div>
                            <div class="stat-value">
                              {formatBytes(p().simulation.download, i18n.locale())}
                            </div>
                          </div>
                        )}
                      </Show>
                    </div>
                    <section class="flex flex-col gap-3">
                      <div class="flex flex-wrap items-center justify-between gap-2">
                        <h2 class="text-lg font-semibold">{t("list.title")}</h2>
                        <button
                          type="button"
                          class="btn btn-primary btn-sm"
                          disabled={actionsOff() || u().items.length === 0}
                          onClick={() => void act(() => setDialog("upgrade"))}
                        >
                          {t("list.installAll")}
                        </button>
                      </div>
                      <UpdateList updates={u()} />
                      <Show when={(lastPlan()?.held.length ?? 0) > 0}>
                        <p class="text-sm text-base-content/70">
                          {t("upgrade.held", { names: lastPlan()?.held.join(", ") ?? "" })}
                        </p>
                      </Show>
                    </section>
                  </>
                )}
              </Show>
              <History jobs={jobs() ?? []} admin={admin()} />
            </>
          )}
        </Match>
      </Switch>
      <Show when={dialog() === "upgrade"}>
        <UpgradeDialog onClose={() => setDialog(undefined)} onStarted={started} onPlan={setLastPlan} />
      </Show>
      <Show when={dialog() === "release" && rels()}>
        {(r) => <ReleaseDialog releases={r()} onClose={() => setDialog(undefined)} onStarted={started} />}
      </Show>
      <ConfirmDialog
        open={dialog() === "resume"}
        title={t("resume.title")}
        message={t("resume.message", { target: st()?.resume?.target ?? "" })}
        confirmLabel={t("resume.confirm")}
        busy={resuming()}
        onConfirm={() => void resume()}
        onCancel={() => setDialog(undefined)}
      />
      <Show when={dialog() === "reboot"}>
        <RebootDialog onClose={() => setDialog(undefined)} />
      </Show>
    </div>
  );
}
