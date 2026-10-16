import { useExtension, useI18n, useSession } from "@edelweiss/sdk";
import { createEffect, createMemo, createSignal, For, on, onCleanup, Show } from "solid-js";
import type { Job, JobStatus, LogEvent } from "./api";
import { formatTime } from "./format";

const MAX_LINES = 5000;

export const statusClass: Record<JobStatus, string> = {
  queued: "badge-ghost",
  running: "badge-info",
  done: "badge-success",
  partial: "badge-warning",
  done_not_persisted: "badge-error",
  failed: "badge-error",
  failed_clean: "badge-warning",
  interrupted: "badge-error",
};

const parse = (e: Event): unknown => {
  const raw = (e as MessageEvent<unknown>).data;
  if (typeof raw !== "string") return undefined; // a failed connection, not an event of the server
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
};

/**
 * The running job: its step, progress and, for admins, its log, live from GET /jobs/:id/events. One stream per job and
 * admin mode; the browser's own reconnect resumes exactly (Last-Event-ID), so the lines stay. A new stream starts with
 * the tail of the log. onEnd gets the final state.
 */
export function JobPanel(props: { job: Job; onEnd: (job: Job) => void }) {
  const ext = useExtension();
  const session = useSession();
  const { t, locale } = useI18n();
  const admin = createMemo(() => session.admin());
  const [job, setJob] = createSignal(props.job);
  const [lines, setLines] = createSignal<string[]>([]);
  const [paused, setPaused] = createSignal(false);
  const [follow, setFollow] = createSignal(true);
  let held: string[] = [];
  let box: HTMLPreElement | undefined;
  const append = (more: string[]) =>
    setLines((prev) => {
      const all = prev.concat(more);
      return all.length > MAX_LINES ? all.slice(-MAX_LINES) : all;
    });
  createEffect(
    on([() => props.job.id, admin], ([id]) => {
      setLines([]);
      held = [];
      const source = new EventSource(`${ext.apiBase}/jobs/${encodeURIComponent(id)}/events`);
      source.addEventListener("state", (e) => {
        const s = parse(e) as Job | undefined;
        if (s) setJob(s);
      });
      source.addEventListener("log", (e) => {
        const ev = parse(e) as LogEvent | undefined;
        if (!ev) return;
        if (paused()) {
          held = held.concat(ev.lines).slice(-MAX_LINES);
          return;
        }
        append(ev.lines);
      });
      source.addEventListener("end", (e) => {
        source.close();
        const s = parse(e) as Job | undefined;
        if (s) {
          setJob(s);
          props.onEnd(s);
        }
      });
      source.onerror = () => {
        if (source.readyState === EventSource.CLOSED) void session.refresh().catch(() => {});
      };
      onCleanup(() => source.close());
    }),
  );
  const togglePause = () => {
    if (paused()) {
      append(held);
      held = [];
    }
    setPaused(!paused());
  };
  createEffect(
    on(lines, () => {
      if (follow() && box) box.scrollTop = box.scrollHeight;
    }),
  );
  const j = job;
  /** The step in words of the user; one the page has no words for (a newer backend) is shown as it is. */
  const stepName = (name: string) => {
    const key = `job.steps.${name}`;
    const text = t(key);
    return text === key ? name : text;
  };
  return (
    <section class="card bg-base-200">
      <div class="card-body gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="card-title">{t(`job.kind.${j().kind}`, { target: j().target ?? "" })}</h2>
          <span class={`badge ${statusClass[j().status]}`}>{t(`job.status.${j().status}`)}</span>
        </div>
        <Show
          when={j().progress && (j().progress?.max ?? 0) > 0}
          fallback={<progress class="progress progress-primary w-full" />}
        >
          <progress
            class="progress progress-primary w-full"
            value={j().progress?.cur ?? 0}
            max={j().progress?.max ?? 1}
          />
        </Show>
        <Show when={j().phase}>
          <p class="text-sm">{t("job.step", { n: j().steps.length + 1, step: stepName(j().phase ?? "") })}</p>
        </Show>
        <Show when={j().current}>
          <p class="font-mono text-sm">{j().current}</p>
        </Show>
        <Show when={j().waiting}>
          <p class="text-sm text-warning">
            {j().lockHolder
              ? t("job.waitingFor", { name: j().lockHolder?.name ?? "", pid: j().lockHolder?.pid ?? "" })
              : t("job.waiting")}
          </p>
        </Show>
        <Show when={j().apkAlive}>
          <p role="alert" class="text-sm text-error">
            {t("job.apkAlive", { pid: j().apkPid ?? "" })}
          </p>
        </Show>
        <p class="text-sm text-base-content/70">
          {t("job.background")}{" "}
          {t("job.started", { time: formatTime(j().started ?? j().created, locale()), user: j().user })}
        </p>
        <Show when={admin()} fallback={<p class="text-sm text-base-content/70">{t("job.logAdminOnly")}</p>}>
          <details open>
            <summary class="cursor-pointer font-semibold">{t("job.log")}</summary>
            <div class="mt-2 flex flex-wrap items-center gap-3">
              <button type="button" class="btn btn-xs" onClick={togglePause}>
                {paused() ? t("job.continue") : t("job.pause")}
              </button>
              <label class="flex items-center gap-1 text-sm">
                <input
                  type="checkbox"
                  class="checkbox checkbox-xs"
                  checked={follow()}
                  onChange={(e) => setFollow(e.currentTarget.checked)}
                />
                {t("job.follow")}
              </label>
            </div>
            <pre ref={box} class="mt-2 h-72 overflow-auto rounded-box bg-base-300 p-2 text-xs leading-5">
              <Show
                when={lines().length > 0}
                fallback={<span class="text-base-content/50">{t("job.noLog")}</span>}
              >
                <For each={lines()}>{(l) => <div>{l}</div>}</For>
              </Show>
            </pre>
          </details>
        </Show>
      </div>
    </section>
  );
}
