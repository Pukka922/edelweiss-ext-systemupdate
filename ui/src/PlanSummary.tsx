import { useI18n } from "@edelweiss/sdk";
import { For, Show } from "solid-js";
import { changesOf, type Plan } from "./api";
import { formatBytes } from "./format";
import { tPlural } from "./plural";

/** The parameters of the blocker texts: apk_busy names who holds apk's lock, latest_stable_ahead the target. */
export const blockerParams = (p: Plan) => ({
  name: p.lockHolder?.name ?? "apk",
  pid: p.lockHolder?.pid ?? "?",
  target: p.target ?? "",
});

/** What a preview found: blockers, warnings and what the simulation would change. */
export function PlanSummary(props: { plan: Plan; hideBlockers?: boolean }) {
  const i18n = useI18n();
  const { t } = i18n;
  const c = () => changesOf(props.plan.simulation);
  const sim = () => props.plan.simulation;
  return (
    <div class="flex flex-col gap-3">
      <Show when={!props.hideBlockers && props.plan.blockers.length > 0}>
        <div role="alert" class="alert alert-error">
          <ul class="list-disc pl-4">
            <For each={props.plan.blockers}>
              {(b) => <li>{t(`blocker.${b}`, blockerParams(props.plan))}</li>}
            </For>
          </ul>
        </div>
      </Show>
      <Show when={props.plan.messages.length > 0}>
        <pre class="overflow-x-auto rounded-box bg-base-200 p-2 text-xs">
          {props.plan.messages.join("\n")}
        </pre>
      </Show>
      <Show when={props.plan.warnings.length > 0}>
        <div class="alert alert-warning">
          <ul class="list-disc pl-4">
            <For each={props.plan.warnings}>{(w) => <li>{t(`warning.${w}`)}</li>}</For>
          </ul>
        </div>
      </Show>
      <Show when={props.plan.blockers.length === 0}>
        <p>{tPlural(i18n, "upgrade.summary", c().all.length)}</p>
        <Show when={sim().sizesKnown}>
          <p class="text-sm">
            {t("upgrade.download", { size: formatBytes(sim().download, i18n.locale()) })}
            {" · "}
            {sim().additional < 0
              ? t("upgrade.freed", { size: formatBytes(sim().additional, i18n.locale()) })
              : t("upgrade.used", { size: formatBytes(sim().additional, i18n.locale()) })}
          </p>
        </Show>
        <Show when={c().removed.length > 0}>
          <div class="alert alert-warning flex-col items-start">
            <span class="font-semibold">{t("upgrade.removed")}</span>
            <ul class="list-disc pl-4 font-mono text-sm">
              <For each={c().removed}>{(r) => <li>{r.name}</li>}</For>
            </ul>
          </div>
        </Show>
        <Show when={c().downgraded.length > 0}>
          <div class="alert alert-warning flex-col items-start">
            <span class="font-semibold">{t("upgrade.downgraded")}</span>
            <ul class="list-disc pl-4 font-mono text-sm">
              <For each={c().downgraded}>{(d) => <li>{`${d.name} ${d.old ?? ""} → ${d.new ?? ""}`}</li>}</For>
            </ul>
          </div>
        </Show>
        <Show when={props.plan.held.length > 0}>
          <p class="text-sm text-base-content/70">
            {t("upgrade.held", { names: props.plan.held.join(", ") })}
          </p>
        </Show>
      </Show>
    </div>
  );
}
