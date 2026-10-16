import { useI18n } from "@edelweiss/sdk";
import { For, Show } from "solid-js";
import type { Advisory, Updates } from "./api";
import { safeHttps } from "./format";
import { tPlural } from "./plural";

function SecurityBadge(props: { advisories: Advisory[] }) {
  const i18n = useI18n();
  return (
    <details class="dropdown">
      <summary
        class="badge badge-error badge-sm cursor-pointer"
        title={props.advisories.map((a) => a.id).join(", ")}
      >
        {tPlural(i18n, "list.security", props.advisories.length)}
      </summary>
      <ul class="dropdown-content menu z-10 rounded-box bg-base-100 p-2 shadow">
        <For each={props.advisories}>
          {(a) => (
            <li>
              <Show when={safeHttps(a.link)} fallback={<span class="font-mono text-xs">{a.id}</span>}>
                {(href) => (
                  <a class="font-mono text-xs" href={href()} target="_blank" rel="noopener noreferrer">
                    {a.id}
                  </a>
                )}
              </Show>
            </li>
          )}
        </For>
      </ul>
    </details>
  );
}

/** The cached list of upgradable packages with their badges. */
export function UpdateList(props: { updates: Updates }) {
  const { t } = useI18n();
  return (
    <Show when={props.updates.checked} fallback={<p class="text-base-content/70">{t("list.notChecked")}</p>}>
      <Show
        when={props.updates.items.length > 0}
        fallback={<p class="text-base-content/70">{t("list.empty")}</p>}
      >
        <div class="overflow-x-auto">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>{t("list.package")}</th>
                <th>{t("list.version")}</th>
                <th>{t("list.notes")}</th>
              </tr>
            </thead>
            <tbody>
              <For each={props.updates.items}>
                {(u) => (
                  <tr>
                    <td class="font-mono">{u.name}</td>
                    <td class="font-mono text-sm">{`${u.old} → ${u.new}`}</td>
                    <td>
                      <div class="flex flex-wrap gap-1">
                        <Show when={u.security.length > 0}>
                          <SecurityBadge advisories={u.security} />
                        </Show>
                        <Show when={u.reboot}>
                          <span class="badge badge-warning badge-sm">{t("list.reboot")}</span>
                        </Show>
                      </div>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
        <Show when={!props.updates.securityData}>
          <p class="text-sm text-base-content/70">{t("list.noSecurityData")}</p>
        </Show>
      </Show>
    </Show>
  );
}
