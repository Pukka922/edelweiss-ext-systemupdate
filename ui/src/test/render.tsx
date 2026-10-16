import { createI18n } from "@edelweiss/sdk";
import { FakeEventSource, fakeSession, TestShell } from "@edelweiss/sdk/testing";
import { render } from "@solidjs/testing-library";
import type { JSX } from "solid-js";
import { vi } from "vitest";
import de from "../../public/locales/de.json";
import en from "../../public/locales/en.json";

export const BASE = "/ext/systemupdate/api";

export const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
export const apiError = (status: number, code: string, message = code) =>
  json({ error: { code, message } }, status);

export type Handler = (body: unknown) => Response | Promise<Response>;
export interface Call {
  method: string;
  path: string;
  body: unknown;
  headers: Record<string, string>;
}

/**
 * Stubs fetch and EventSource. Routes are "METHOD /path": a path below the extension's API without its base
 * ("GET /status"), or an absolute path of the core ("POST /api/system/reboot"). Unknown routes answer 404.
 */
export function stubApi(routes: Record<string, Handler>) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? "GET";
    const path = url.startsWith(BASE) ? url.slice(BASE.length) : url;
    const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ method, path, body, headers: (init.headers ?? {}) as Record<string, string> });
    const handler = routes[`${method} ${path}`];
    return handler ? handler(body) : new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("EventSource", FakeEventSource);
  FakeEventSource.instances.length = 0;
  return {
    calls,
    fetchMock,
    posts: (path: string) => calls.filter((c) => c.method === "POST" && c.path === path),
  };
}

/** Renders ui inside the test shell of the extension, with the English and German texts. */
export function renderExt(ui: () => JSX.Element, session = fakeSession({ admin: true, canAdmin: true })) {
  const i18n = createI18n("en");
  i18n.register("systemupdate", "en", en);
  i18n.register("systemupdate", "de", de);
  const view = render(() => (
    <TestShell extension="systemupdate" i18n={i18n} session={session.api}>
      {ui()}
    </TestShell>
  ));
  return { ...view, i18n, session };
}

/** The newest EventSource the code under test opened. */
export const lastSource = () => FakeEventSource.instances[FakeEventSource.instances.length - 1];
