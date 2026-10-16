import { useApi, useI18n } from "@edelweiss/sdk";
import { createSignal, For, Show } from "solid-js";
import type { Job, Plan, Releases } from "./api";
import { errorText } from "./errors";
import { branchNewer, formatDate, safeHttps } from "./format";
import { Modal } from "./Modal";
import { blockerParams, PlanSummary } from "./PlanSummary";

/**
 * The pre-flight checks the preview runs, with the blocker codes of each, in the order PreviewRelease runs them in
 * stages: the early ones (stage 0), then the lock and the update of the repositories (1, 2), then the space and the
 * simulation (3). The preview returns at the first stage with a blocker, so a later check did not run.
 */
const CHECKS: { key: string; codes: string[]; stage: number }[] = [
  { key: "mode", codes: ["mode_not_sys"], stage: 0 },
  { key: "target", codes: ["target_not_allowed"], stage: 0 },
  { key: "repos", codes: ["repos_inconsistent", "extra_repos_mappable", "latest_stable_ahead"], stage: 0 },
  { key: "usr", codes: ["usr_separate"], stage: 0 },
  { key: "boot", codes: ["boot_not_mounted", "boot_read_only"], stage: 0 },
  { key: "lock", codes: ["apk_busy"], stage: 1 },
  { key: "mirror", codes: ["repos_unavailable"], stage: 2 },
  { key: "space", codes: ["no_space_root", "no_space_boot"], stage: 3 },
  { key: "simulation", codes: ["simulation_failed"], stage: 3 },
];

function Checklist(props: { plan: Plan }) {
  const { t } = useI18n();
  const blocks = (c: (typeof CHECKS)[number]) => c.codes.some((x) => props.plan.blockers.includes(x));
  const stopped = () => Math.min(...CHECKS.filter(blocks).map((c) => c.stage), Number.POSITIVE_INFINITY);
  return (
    <div>
      <p class="font-semibold">{t("release.checks")}</p>
      <ul class="flex flex-col gap-1">
        <For each={CHECKS}>
          {(c) => {
            const failed = () => c.codes.filter((x) => props.plan.blockers.includes(x));
            const skipped = () => c.stage > stopped();
            return (
              <li class="flex gap-2">
                <span aria-hidden="true">{skipped() ? "–" : failed().length > 0 ? "✗" : "✓"}</span>
                <span>
                  {t(`release.item.${c.key}`)}
                  <Show when={skipped()}>
                    <span class="text-base-content/60"> ({t("release.skipped")})</span>
                  </Show>
                  <For each={failed()}>
                    {(x) => (
                      <span class="block text-sm text-error">
                        {t(`blocker.${x}`, blockerParams(props.plan))}
                      </span>
                    )}
                  </For>
                </span>
              </li>
            );
          }}
        </For>
      </ul>
    </div>
  );
}

/** The release upgrade: choose the target, run the pre-flight checks and the simulation, then start the job. */
export function ReleaseDialog(props: {
  releases: Releases;
  onClose: () => void;
  onStarted: (job: Job) => void;
}) {
  const api = useApi();
  const { t, locale } = useI18n();
  const stable = () => props.releases.targets.filter((x) => !x.edge);
  const edge = () => props.releases.targets.find((x) => x.edge);
  // only edge is offered although a newer stable release exists: the branch right after the installed one has reached
  // its end of life (the page never offers a jump over a branch)
  const nextEnded = () =>
    stable().length === 0 && !!edge() && branchNewer(props.releases.latest, props.releases.current);
  const [target, setTarget] = createSignal(stable()[0]?.branch ?? edge()?.branch ?? "");
  const [typed, setTyped] = createSignal("");
  const [plan, setPlan] = createSignal<Plan>();
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string>();
  const edgeOk = () => target() !== "edge" || typed() === "edge";
  const choose = (branch: string) => {
    setTarget(branch);
    setPlan(undefined); // a plan is for one target
    setTyped("");
  };
  const run = async (f: () => Promise<void>) => {
    if (busy()) return;
    setBusy(true);
    setError(undefined);
    try {
      await f();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  };
  const check = () =>
    run(async () => {
      setPlan(undefined);
      setPlan(await api.post<Plan>("/preview", { kind: "release", target: target() }));
    });
  const start = () =>
    run(async () => {
      const p = plan();
      if (!p) return;
      // the server wants the typed word of edge as well, not only this dialog
      const body = { kind: "release", planId: p.id, ...(p.target === "edge" ? { confirm: typed() } : {}) };
      props.onStarted(await api.post<Job>("/jobs", body));
    });
  const canStart = () => !busy() && edgeOk() && plan()?.target === target() && plan()?.blockers.length === 0;
  return (
    <Modal
      title={t("release.title")}
      busy={busy()}
      wide
      onClose={props.onClose}
      actions={
        <>
          <button type="button" class="btn" disabled={busy()} onClick={() => props.onClose()}>
            {t("common.cancel")}
          </button>
          <Show when={target()}>
            <button type="button" class="btn btn-error" disabled={!canStart()} onClick={() => void start()}>
              {t("release.start", { branch: target() })}
            </button>
          </Show>
        </>
      }
    >
      <p>{t("release.current", { branch: props.releases.current })}</p>
      <Show
        when={props.releases.available}
        fallback={<p class="text-base-content/70">{t("release.noList")}</p>}
      >
        <Show
          when={props.releases.targets.length > 0}
          fallback={<p class="text-base-content/70">{t("release.noTargets")}</p>}
        >
          <Show when={nextEnded()}>
            <div class="alert alert-warning">
              {t("release.nextEnded", { current: props.releases.current })}
            </div>
          </Show>
          <fieldset class="flex flex-col gap-2">
            <Show when={stable().length > 0}>
              <legend class="font-semibold">{t("release.stable")}</legend>
            </Show>
            <For each={stable()}>
              {(x) => (
                <label class="flex flex-wrap items-center gap-2">
                  <input
                    type="radio"
                    class="radio radio-sm"
                    name="target"
                    checked={target() === x.branch}
                    disabled={busy()}
                    onChange={() => choose(x.branch)}
                  />
                  <span>
                    {t("release.target", {
                      branch: x.branch,
                      latest: x.latest ?? "",
                      eol: x.eol ? formatDate(x.eol, locale()) : "",
                    })}
                  </span>
                  <Show when={safeHttps(x.notes)}>
                    {(href) => (
                      <a class="link text-sm" href={href()} target="_blank" rel="noopener noreferrer">
                        {t("release.notes")}
                      </a>
                    )}
                  </Show>
                </label>
              )}
            </For>
            <Show when={edge()}>
              <p class="mt-2 font-semibold">{t("release.edgeGroup")}</p>
              <label class="flex items-center gap-2">
                <input
                  type="radio"
                  class="radio radio-sm radio-error"
                  name="target"
                  checked={target() === "edge"}
                  disabled={busy()}
                  onChange={() => choose("edge")}
                />
                <span>{t("release.edgeLabel")}</span>
              </label>
            </Show>
          </fieldset>
          <Show when={target() === "edge"}>
            <div role="alert" class="alert alert-error">
              {t("release.edgeWarning")}
            </div>
            <label class="flex flex-col gap-1">
              <span>{t("release.edgeConfirm")}</span>
              <input
                class="input input-sm"
                autocomplete="off"
                disabled={busy()}
                value={typed()}
                onInput={(e) => setTyped(e.currentTarget.value)}
              />
            </label>
          </Show>
          <div>
            <button
              type="button"
              class="btn btn-sm"
              disabled={busy() || !edgeOk()}
              onClick={() => void check()}
            >
              {busy() && !plan() ? t("release.checking") : t("release.check")}
            </button>
          </div>
          <Show when={plan()}>
            {(p) => (
              <>
                <Checklist plan={p()} />
                <PlanSummary plan={p()} hideBlockers />
              </>
            )}
          </Show>
        </Show>
      </Show>
      <Show when={error()}>
        <p role="alert" class="text-error">
          {error()}
        </p>
      </Show>
    </Modal>
  );
}
