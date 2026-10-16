import { fakeSession, sessionInfo } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Job } from "./api";
import { JobPanel } from "./JobPanel";
import { job } from "./test/fixtures";
import { lastSource, renderExt, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const emit = (name: string, data: unknown) => lastSource()?.emit(name, JSON.stringify(data));

describe("JobPanel", () => {
  it("shows progress and log lines and reports the end", async () => {
    stubApi({});
    const ended: Job[] = [];
    const view = renderExt(() => <JobPanel job={job()} onEnd={(j) => ended.push(j)} />);
    lastSource()?.open();
    emit(
      "state",
      job({ progress: { cur: 1, max: 4 }, current: "(1/2) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)" }),
    );
    emit("log", {
      lines: ["fetch https://dl-cdn.alpinelinux.org/alpine/v3.23/main", "(1/2) Upgrading curl"],
      offset: 80,
    });
    expect(await view.findByText("(1/2) Upgrading curl")).toBeTruthy();
    expect((view.getByRole("progressbar") as HTMLProgressElement).value).toBe(1);
    expect(view.getByText("(1/2) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)")).toBeTruthy();
    emit("end", job({ status: "done" }));
    await waitFor(() => expect(ended.map((j) => j.status)).toEqual(["done"]));
    expect(lastSource()?.readyState).toBe(2); // closed: no reconnect after the end
  });

  it("names who holds the apk lock", async () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job()} onEnd={() => {}} />);
    emit("state", job({ waiting: true, lockHolder: { pid: 1234, name: "apk" } }));
    expect(await view.findByText("Waiting for apk (pid 1234) to finish …")).toBeTruthy();
  });

  it("shows the log to admins only and opens a new stream when admin mode changes", async () => {
    stubApi({});
    const session = fakeSession({ admin: false, canAdmin: true });
    const view = renderExt(() => <JobPanel job={job()} onEnd={() => {}} />, session);
    expect(view.getByText("Turn on admin mode to see the log.")).toBeTruthy();
    const first = lastSource();
    session.setInfo(sessionInfo({ admin: true, canAdmin: true }));
    await waitFor(() => expect(lastSource()).not.toBe(first));
    expect(first?.readyState).toBe(2);
    emit("log", { lines: ["a line"], offset: 7 });
    expect(await view.findByText("a line")).toBeTruthy();
  });

  it("keeps the lines when the browser reconnects, and keeps at most 5000", async () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job()} onEnd={() => {}} />);
    const many = Array.from({ length: 5003 }, (_, i) => `line ${i}`);
    emit("log", { lines: many, offset: 99999 });
    expect(await view.findByText("line 5002")).toBeTruthy();
    expect(view.queryByText("line 2")).toBeNull();
    lastSource()?.fail(false);
    lastSource()?.open();
    expect(view.getByText("line 5002")).toBeTruthy();
  });

  it("names the step in words, not by the name of the program step", () => {
    stubApi({});
    const steps: Record<string, string> = {
      "self-upgrade": "Updating apk-tools",
      upgrade: "Installing updates",
      "current-self-upgrade": "Updating apk-tools of the current release",
      "current-upgrade": "Updating the current release",
      update: "Fetching the package indexes",
      "restore-update": "Restoring the package indexes",
      "cache-clean": "Cleaning the package cache",
      "lbu-commit": "Saving the changes (lbu commit)",
    };
    for (const [name, words] of Object.entries(steps)) {
      const view = renderExt(() => <JobPanel job={job({ phase: name, steps: [] })} onEnd={() => {}} />);
      expect(view.getByText(`Step 1: ${words}`)).toBeTruthy();
      cleanup();
    }
  });

  it("names the steps in German too", () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job({ phase: "lbu-commit", steps: [] })} onEnd={() => {}} />);
    view.i18n.setLocale("de");
    expect(view.getByText("Schritt 1: Änderungen werden gesichert (lbu commit)")).toBeTruthy();
  });

  it("shows a step it has no words for as it is", () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job({ phase: "frobnicate", steps: [] })} onEnd={() => {}} />);
    expect(view.getByText("Step 1: frobnicate")).toBeTruthy();
  });

  it("keeps the log in a section that can be collapsed", () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job()} onEnd={() => {}} />);
    const details = view.container.querySelector("details");
    expect(details?.open).toBe(true);
    expect(details?.querySelector("summary")?.textContent).toBe("Log");
    expect(details?.contains(view.getByRole("button", { name: "Pause" }))).toBe(true);
    expect(details?.querySelector("pre")).toBeTruthy();
  });

  it("holds lines while paused", async () => {
    stubApi({});
    const view = renderExt(() => <JobPanel job={job()} onEnd={() => {}} />);
    fireEvent.click(view.getByRole("button", { name: "Pause" }));
    emit("log", { lines: ["later"], offset: 6 });
    await new Promise((r) => setTimeout(r, 10));
    expect(view.queryByText("later")).toBeNull();
    fireEvent.click(view.getByRole("button", { name: "Continue" }));
    expect(await view.findByText("later")).toBeTruthy();
  });
});
