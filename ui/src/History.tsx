import { useExtension, useI18n } from "@edelweiss/sdk";
import { For, Show } from "solid-js";
import type { Job } from "./api";
import { formatDuration, formatTime } from "./format";
import { statusClass } from "./JobPanel";

/** The last 20 jobs; admins can open each log. */
export function History(props: { jobs: Job[]; admin: boolean }) {
  const ext = useExtension();
  const { t, locale } = useI18n();
  return (
    <section class="flex flex-col gap-2">
      <h2 class="text-lg font-semibold">{t("history.title")}</h2>
      <Show when={props.jobs.length > 0} fallback={<p class="text-base-content/70">{t("history.empty")}</p>}>
        <div class="overflow-x-auto">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>{t("history.job")}</th>
                <th>{t("history.started")}</th>
                <th>{t("history.duration")}</th>
                <th>{t("history.user")}</th>
                <th>{t("history.result")}</th>
                <Show when={props.admin}>
                  <th>{t("history.log")}</th>
                </Show>
              </tr>
            </thead>
            <tbody>
              <For each={props.jobs}>
                {(j) => (
                  <tr>
                    <td>{t(`job.kind.${j.kind}`, { target: j.target ?? "" })}</td>
                    <td>{formatTime(j.started ?? j.created, locale())}</td>
                    <td>
                      {j.started && j.ended
                        ? formatDuration(Date.parse(j.ended) - Date.parse(j.started))
                        : "–"}
                    </td>
                    <td>{j.user}</td>
                    <td>
                      <span class={`badge badge-sm ${statusClass[j.status]}`}>
                        {t(`job.status.${j.status}`)}
                      </span>
                    </td>
                    <Show when={props.admin}>
                      <td>
                        <a
                          class="link"
                          href={`${ext.apiBase}/jobs/${encodeURIComponent(j.id)}/log`}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {t("history.log")}
                        </a>
                      </td>
                    </Show>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
    </section>
  );
}
