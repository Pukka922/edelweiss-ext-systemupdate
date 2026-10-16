import { LoadingState, useApi, useI18n } from "@edelweiss/sdk";
import { createResource, createSignal, Match, Show, Switch } from "solid-js";
import type { Job, Plan } from "./api";
import { errorText } from "./errors";
import { Modal } from "./Modal";
import { PlanSummary } from "./PlanSummary";

/** "Install all": the simulation first, then the job. */
export function UpgradeDialog(props: {
  onClose: () => void;
  onStarted: (job: Job) => void;
  onPlan?: (p: Plan) => void;
}) {
  const api = useApi();
  const { t } = useI18n();
  const [plan] = createResource(async () => {
    const p = await api.post<Plan>("/preview", { kind: "upgrade" });
    props.onPlan?.(p);
    return p;
  });
  const loaded = () => (plan.error ? undefined : plan());
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string>();
  const start = async () => {
    const p = loaded();
    if (!p || busy()) return;
    setBusy(true);
    setError(undefined);
    try {
      props.onStarted(await api.post<Job>("/jobs", { kind: "upgrade", planId: p.id }));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={t("upgrade.title")}
      busy={busy()}
      wide
      onClose={props.onClose}
      actions={
        <>
          <button type="button" class="btn" disabled={busy()} onClick={() => props.onClose()}>
            {t("common.cancel")}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            disabled={busy() || !loaded() || (loaded()?.blockers.length ?? 0) > 0}
            onClick={() => void start()}
          >
            {t("upgrade.confirm")}
          </button>
        </>
      }
    >
      <Switch fallback={<LoadingState label={t("upgrade.simulating")} />}>
        <Match when={plan.error}>
          <p role="alert" class="text-error">
            {errorText(plan.error, t)}
          </p>
        </Match>
        <Match when={loaded()}>{(p) => <PlanSummary plan={p()} />}</Match>
      </Switch>
      <Show when={error()}>
        <p role="alert" class="text-error">
          {error()}
        </p>
      </Show>
    </Modal>
  );
}
