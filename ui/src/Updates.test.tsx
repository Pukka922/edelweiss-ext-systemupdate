import { FakeEventSource, fakeSession } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { job, plan, post, releases, status, updates } from "./test/fixtures";
import { apiError, json, lastSource, renderExt, stubApi } from "./test/render";
import { Updates } from "./Updates";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** The "Restart …" buttons on the page: the banner's, and the one of the result panel when it offers it. */
const restartButtons = (view: { queryAllByRole: (role: string, opts: { name: string }) => HTMLElement[] }) =>
  view.queryAllByRole("button", { name: "Restart …" }) as HTMLButtonElement[];

const base = (over: Record<string, () => Response> = {}) => ({
  "GET /status": () => json(status()),
  "GET /updates": () => json(updates()),
  "GET /releases": () => json(releases()),
  "GET /jobs": () => json({ jobs: [] }),
  ...over,
});

describe("Updates", () => {
  it("shows the system, the banners and the list", async () => {
    stubApi(base());
    const view = renderExt(() => <Updates />);
    expect(await view.findByText("Alpine 3.23.6 · x86_64 · installed to disk")).toBeTruthy();
    expect(view.getByText(/Alpine v3.24 is available/)).toBeTruthy();
    expect(view.getByText(/The community repository of Alpine v3.23/)).toBeTruthy();
    expect(view.getByText("curl")).toBeTruthy();
    expect(view.getByText("8.16.0-r0 → 8.16.0-r1")).toBeTruthy();
    expect(view.getByText("Security")).toBeTruthy();
    expect(view.getByText("Restart")).toBeTruthy(); // the badge of linux-lts
  });

  it("asks for admin mode before it installs", async () => {
    const api = stubApi(base());
    const session = fakeSession({ admin: false, canAdmin: true });
    const view = renderExt(() => <Updates />, session);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    await waitFor(() => expect(session.calls.requestAdmin).toBe(1));
    expect(api.posts("/preview")).toHaveLength(0);
  });

  it("shows the simulation and starts the upgrade", async () => {
    const api = stubApi(
      base({
        "POST /preview": () => json(plan()),
        "POST /jobs": () => json(job(), 202),
      }),
    );
    const view = renderExt(() => <Updates />);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    expect(await view.findByText("2 packages change.")).toBeTruthy();
    expect(view.getByText("These packages are removed:")).toBeTruthy();
    expect(view.getByText("gtk+2.0")).toBeTruthy();
    expect(view.getByText(/Download: 386 MiB/)).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Install" }));
    await waitFor(() => expect(api.posts("/jobs")).toHaveLength(1));
    expect(api.posts("/jobs")[0]?.body).toEqual({ kind: "upgrade", planId: "0a1b2c3d4e5f6071" });
    expect(await view.findByText("Install updates")).toBeTruthy(); // the job panel
    expect(lastSource()?.url).toBe("/ext/systemupdate/api/jobs/20261013T120000.000-0a1b2c3d/events");
  });

  it("does not install when the preview blocks it", async () => {
    stubApi(base({ "POST /preview": () => json(plan({ blockers: ["no_space_root"] })) }));
    const view = renderExt(() => <Updates />);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    expect(await view.findByText("There is not enough free space on /.")).toBeTruthy();
    expect((view.getByRole("button", { name: "Install" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("follows the job that runs when the page opens", async () => {
    stubApi(base({ "GET /status": () => json(status({ current: job({ kind: "refresh" }) })) }));
    const view = renderExt(() => <Updates />);
    expect(await view.findByText("Check for updates")).toBeTruthy();
    expect(lastSource()?.url).toContain("/jobs/20261013T120000.000-0a1b2c3d/events");
    expect((view.getByRole("button", { name: "Check now" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the result when the job ends, and does not follow the ended job again", async () => {
    let over = false;
    stubApi(
      base({
        // until the new status arrives, the old one still reports the job as current
        "GET /status": () =>
          json(
            over ? status({ last: job({ status: "done" }) }) : status({ current: job({ kind: "upgrade" }) }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText("Install updates")).toBeTruthy();
    expect(FakeEventSource.instances).toHaveLength(1);
    over = true;
    lastSource()?.emit("end", JSON.stringify(job({ status: "done" })));
    expect(await view.findByText("The job finished.")).toBeTruthy();
    expect(view.queryByText("Install updates")).toBeNull(); // the panel is gone
    expect(FakeEventSource.instances).toHaveLength(1); // and no stream for the ended job was opened again
  });

  it("treats an interrupted job whose apk still runs as busy", async () => {
    const api = stubApi(
      base({
        "GET /status": () =>
          json(
            status({
              checked: undefined,
              reboot: true,
              last: job({ status: "interrupted", apkAlive: true, apkPid: 4711 }),
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText(/apk still runs \(pid 4711\)/)).toBeTruthy();
    expect((view.getByRole("button", { name: "Install all" }) as HTMLButtonElement).disabled).toBe(true);
    expect((view.getByRole("button", { name: "Check now" }) as HTMLButtonElement).disabled).toBe(true);
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true]); // the banner's
    await new Promise((r) => setTimeout(r, 30));
    expect(api.posts("/refresh")).toHaveLength(0); // not even the automatic check: it would only answer 409
  });

  it("keeps the restart button of the result disabled while apk of a dead runner still runs", async () => {
    stubApi(
      base({
        "GET /status": () =>
          json(
            status({
              reboot: true,
              last: job({ status: "interrupted", apkAlive: true, apkPid: 4711, post: post() }),
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText(/apk still runs \(pid 4711\)/)).toBeTruthy();
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true, true]); // the banner's and the result's
  });

  it("polls while apk of a dead runner still runs, and stops when it has finished", async () => {
    let alive = true;
    const api = stubApi(
      base({
        "GET /status": () =>
          json(status({ last: job({ status: "interrupted", apkAlive: alive, apkPid: 4711 }) })),
      }),
    );
    const gets = (path: string) => api.calls.filter((c) => c.method === "GET" && c.path === path).length;
    const view = renderExt(() => <Updates apkPollMs={5} />);
    expect(await view.findByText(/apk still runs \(pid 4711\)/)).toBeTruthy();
    await waitFor(() => expect(gets("/status")).toBeGreaterThan(3));
    expect(gets("/jobs")).toBeGreaterThan(3);
    alive = false;
    await waitFor(() =>
      expect((view.getByRole("button", { name: "Install all" }) as HTMLButtonElement).disabled).toBe(false),
    );
    expect(view.queryByText(/apk still runs/)).toBeNull();
    const [status0, jobs0] = [gets("/status"), gets("/jobs")];
    await new Promise((r) => setTimeout(r, 60));
    expect([gets("/status"), gets("/jobs")]).toEqual([status0, jobs0]); // the poll is over
  });

  it("keeps the restart blocked after a partial release upgrade, also after a later check", async () => {
    stubApi(
      base({
        "GET /status": () =>
          json(
            status({
              reboot: true,
              resume: { target: "v3.24", reason: "partial" },
              last: job({ kind: "refresh", status: "done" }),
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText(/The release upgrade to v3.24 stopped halfway/)).toBeTruthy();
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true]);
  });

  it("keeps the restart behind grub-install after a release upgrade, whatever job came last", async () => {
    const refresh = job({ id: "20261013T130000.000-bbbbbbbb", kind: "refresh", status: "done" });
    const release = job({
      id: "20261013T120000.000-aaaaaaaa",
      kind: "release",
      target: "v3.24",
      status: "done",
      post: post({ grub: true, grubEfi: true }),
    });
    stubApi(
      base({
        "GET /status": () => json(status({ reboot: true, last: refresh })),
        "GET /jobs": () => json({ jobs: [refresh, release] }),
      }),
    );
    const view = renderExt(() => <Updates />);
    const box = await view.findByLabelText("I have run grub-install"); // the result of the release job
    expect(view.getByText("grub-install --target=x86_64-efi --efi-directory=/boot/efi")).toBeTruthy();
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true, true]); // the banner's and the result's
    fireEvent.click(box);
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([false, false]);
  });

  it("keeps the restart blocked, with a banner, while the boot files are stale, whatever job came last", async () => {
    // the live check of /status, not the post-checks of a job: a later check (the last job is a refresh) does not
    // forget a failed initramfs
    stubApi(
      base({
        "GET /status": () =>
          json(status({ reboot: true, bootStale: true, last: job({ kind: "refresh", status: "done" }) })),
      }),
    );
    const view = renderExt(() => <Updates />);
    const banner = await view.findByText(
      "Do NOT restart: the boot files of the new kernel are missing or older than the kernel. Check /boot first.",
    );
    expect(banner.closest(".alert-error")).toBeTruthy();
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true]); // the banner's
  });

  it("does not block the restart for the stale post of an old job once the boot files are fine", async () => {
    // a kernel upgrade whose initramfs failed, which the administrator has fixed since: bootStale is false again
    stubApi(
      base({
        "GET /status": () =>
          json(
            status({
              reboot: true,
              bootStale: false,
              last: job({ status: "done", post: post({ bootOk: false }) }),
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    // the result of that job still says what happened to it, for information
    expect(
      await view.findByText(/Do NOT restart: the boot files of the new kernel are missing or old\./),
    ).toBeTruthy();
    expect(view.queryByText(/missing or older than the kernel/)).toBeNull(); // no banner
    const buttons = restartButtons(view);
    expect(buttons.length).toBeGreaterThan(0);
    expect(buttons.map((b) => b.disabled)).toEqual(buttons.map(() => false));
  });

  it("does not block the restart for a kernel an earlier job installed when the boot files are fine", async () => {
    // a kernel upgraded without a restart, then "Install all": the last job changed no kernel
    stubApi(
      base({
        "GET /status": () =>
          json(
            status({
              reboot: true,
              bootStale: false,
              last: job({ status: "done", post: post({ kernelChanged: false, bootOk: true }) }),
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    await view.findByText("curl");
    expect(view.queryByText(/Do NOT restart/)).toBeNull();
    const buttons = restartButtons(view);
    expect(buttons.length).toBeGreaterThan(0);
    expect(buttons.map((b) => b.disabled)).toEqual(buttons.map(() => false));
  });

  it("keeps the restart blocked after a partial release upgrade that only the history tells about", async () => {
    // no status.resume (the repositories do not say so), and the last job is a later check
    const refresh = job({ id: "20261013T130000.000-bbbbbbbb", kind: "refresh", status: "done" });
    const release = job({
      id: "20261013T120000.000-aaaaaaaa",
      kind: "release",
      target: "v3.24",
      status: "partial",
      reposSwitched: true,
      post: post(),
    });
    stubApi(
      base({
        "GET /status": () => json(status({ reboot: true, last: refresh })),
        "GET /jobs": () => json({ jobs: [refresh, release] }),
      }),
    );
    const view = renderExt(() => <Updates />);
    await view.findByText("Release upgrade to v3.24"); // the history has loaded
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true]); // only the banner's
  });

  it("names the job that runs when another one is refused", async () => {
    stubApi(
      base({
        "POST /preview": () =>
          json(
            {
              error: {
                code: "busy",
                message: "another job runs",
                job: job({ kind: "release", target: "v3.24" }),
              },
            },
            409,
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    expect(await view.findByText("Another job runs: Release upgrade to v3.24.")).toBeTruthy();
  });

  it("says that another job runs when the answer does not name it", async () => {
    stubApi(base({ "POST /preview": () => apiError(409, "busy", "another job runs") }));
    const view = renderExt(() => <Updates />);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    expect(await view.findByText("Another job runs.")).toBeTruthy();
  });

  it("shows an unknown kind of the running job as it is", async () => {
    stubApi(
      base({
        "POST /preview": () =>
          json({ error: { code: "busy", message: "another job runs", job: { kind: "frobnicate" } } }, 409),
      }),
    );
    const view = renderExt(() => <Updates />);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    expect(await view.findByText("Another job runs: frobnicate.")).toBeTruthy();
  });

  it("shows a check that found only part of the repositories as that, not as an upgrade", async () => {
    stubApi(
      base({
        "GET /status": () => json(status({ last: job({ kind: "refresh", status: "partial" }) })),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(
      await view.findByText("Some repositories could not be checked: the list may be incomplete."),
    ).toBeTruthy();
    expect(view.queryByText(/The job finished with errors/)).toBeNull();
  });

  it("shows a failed check as such, for instance without network", async () => {
    stubApi(
      base({ "GET /status": () => json(status({ last: job({ kind: "refresh", status: "failed" }) })) }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText("The check failed.")).toBeTruthy();
    expect(view.queryByText("The job failed.")).toBeNull();
  });

  it("keeps the restart blocked while the history, which tells about a release upgrade, is unknown", async () => {
    const api = stubApi(
      base({
        "GET /status": () => json(status({ reboot: true })),
        "GET /jobs": () => apiError(500, "internal", "no history"),
      }),
    );
    const view = renderExt(() => <Updates />);
    await view.findByText("curl");
    await waitFor(() => expect(api.calls.some((c) => c.path === "/jobs")).toBe(true));
    await new Promise((r) => setTimeout(r, 30));
    expect(restartButtons(view).map((b) => b.disabled)).toEqual([true]);
  });

  it("links advisories only over https", async () => {
    const item = (security: { id: string; link: string }[]) => ({
      name: "curl",
      old: "8.16.0-r0",
      new: "8.16.0-r1",
      origin: "curl",
      security,
      reboot: false,
    });
    stubApi(
      base({
        "GET /updates": () =>
          json(
            updates({
              items: [
                item([
                  { id: "CVE-2026-2222", link: "https://security.alpinelinux.org/vuln/CVE-2026-2222" },
                  { id: "CVE-2026-3333", link: "javascript:alert(1)" },
                ]),
              ],
            }),
          ),
      }),
    );
    const view = renderExt(() => <Updates />);
    await view.findByText("curl");
    expect(view.getByText("CVE-2026-2222").closest("a")?.getAttribute("href")).toBe(
      "https://security.alpinelinux.org/vuln/CVE-2026-2222",
    );
    expect(view.getByText("CVE-2026-3333").closest("a")).toBeNull(); // shown as text
    expect(view.container.querySelector("a[href^='javascript:']")).toBeNull();
  });

  it("checks for updates when the last check is older than an hour", async () => {
    const old = new Date(Date.now() - 2 * 3600_000).toISOString();
    const api = stubApi(
      base({
        "GET /status": () => json(status({ checked: old })),
        "POST /refresh": () => json(job({ kind: "refresh" }), 202),
      }),
    );
    renderExt(() => <Updates />);
    await waitFor(() => expect(api.posts("/refresh")).toHaveLength(1));
  });

  it("does not check by itself after a recent check", async () => {
    const api = stubApi(base());
    const view = renderExt(() => <Updates />);
    await view.findByText("curl");
    await new Promise((r) => setTimeout(r, 30));
    expect(api.posts("/refresh")).toHaveLength(0);
  });

  it("does not check by itself without admin mode", async () => {
    const api = stubApi(base({ "GET /status": () => json(status({ checked: undefined })) }));
    const view = renderExt(() => <Updates />, fakeSession({ admin: false, canAdmin: true }));
    await view.findByText("curl");
    await new Promise((r) => setTimeout(r, 30));
    expect(api.posts("/refresh")).toHaveLength(0);
    expect(view.getByText("Not checked yet")).toBeTruthy();
  });

  it("offers to resume a release upgrade", async () => {
    const api = stubApi(
      base({
        "GET /status": () => json(status({ resume: { target: "v3.24", reason: "partial" } })),
        "POST /jobs": () => json(job({ kind: "resume", target: "v3.24" }), 202),
      }),
    );
    const view = renderExt(() => <Updates />);
    expect(await view.findByText(/The release upgrade to v3.24 stopped halfway/)).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Resume …" }));
    fireEvent.click(await view.findByRole("button", { name: "Resume" }));
    await waitFor(() => expect(api.posts("/jobs")[0]?.body).toEqual({ kind: "resume" }));
  });

  it("explains a release upgrade of a diskless system instead of offering it", async () => {
    stubApi(base({ "GET /status": () => json(status({ mode: "diskless" })) }));
    const view = renderExt(() => <Updates />);
    expect(await view.findByText(/new boot media/)).toBeTruthy();
    expect(view.queryByRole("button", { name: "Plan release upgrade …" })).toBeNull();
  });

  it("says that the next release has ended, with no button, instead of offering the upgrade", async () => {
    const ended = { kind: "next_release_ended", branch: "v3.20", date: "2026-04-01" } as const;
    stubApi(base({ "GET /status": () => json(status({ banners: [ended] })) }));
    const view = renderExt(() => <Updates />);
    const banner = await view.findByText(
      "Alpine v3.20, the next release after the installed one, reached its end of life on Apr 1, 2026: " +
        "this page offers no release upgrade from here. Upgrade one branch at a time by hand " +
        "(see the Alpine release notes).",
    );
    const alert = banner.closest(".alert");
    expect(alert?.classList.contains("alert-warning")).toBe(true);
    expect(alert?.querySelector("button")).toBeNull();
    expect(view.queryByRole("button", { name: "Plan release upgrade …" })).toBeNull();
    view.i18n.setLocale("de");
    expect(
      view.getByText(
        "Alpine v3.20, die nächste Version nach der installierten, hat am 01.04.2026 ihr Support-Ende erreicht: " +
          "Diese Seite bietet von hier aus keinen Versionswechsel an. Den Versionswechsel bitte von Hand vornehmen, " +
          "einen Zweig nach dem anderen (siehe die Versionshinweise von Alpine).",
      ),
    ).toBeTruthy();
  });

  describe("for a user who cannot become admin", () => {
    const hint = "Updates are installed by an administrator in admin mode.";
    const withResult = () =>
      base({
        "GET /status": () =>
          json(
            status({
              reboot: true,
              resume: { target: "v3.24", reason: "partial" },
              last: job({ status: "done", post: post() }),
            }),
          ),
      });

    it("disables the actions and says why", async () => {
      const api = stubApi(withResult());
      const session = fakeSession({ admin: false, canAdmin: false });
      const view = renderExt(() => <Updates />, session);
      expect(await view.findByText(hint)).toBeTruthy();
      const names = ["Check now", "Install all", "Plan release upgrade …", "Resume …"];
      expect(names.map((name) => (view.getByRole("button", { name }) as HTMLButtonElement).disabled)).toEqual(
        [true, true, true, true],
      );
      expect(restartButtons(view).map((b) => b.disabled)).toEqual([true, true]); // the banner's and the result's
      for (const name of names) fireEvent.click(view.getByRole("button", { name }));
      await new Promise((r) => setTimeout(r, 30));
      expect(session.calls.requestAdmin).toBe(0);
      expect(api.calls.filter((c) => c.method === "POST")).toEqual([]);
    });

    it("says it in German too", async () => {
      stubApi(base());
      const view = renderExt(() => <Updates />, fakeSession({ admin: false, canAdmin: false }));
      await view.findByText(hint);
      view.i18n.setLocale("de");
      expect(
        view.getByText("Updates werden im Admin-Modus von einem Administrator installiert."),
      ).toBeTruthy();
    });

    it("leaves the actions and the hint to a user who can ask for admin mode", async () => {
      stubApi(withResult());
      const view = renderExt(() => <Updates />, fakeSession({ admin: false, canAdmin: true }));
      await view.findByText("curl");
      expect(view.queryByText(hint)).toBeNull();
      // the restart waits for the resume of the partial release upgrade, whoever the user is
      expect(restartButtons(view).map((b) => b.disabled)).toEqual([true, true]);
      for (const name of ["Check now", "Install all", "Plan release upgrade …", "Resume …"]) {
        expect((view.getByRole("button", { name }) as HTMLButtonElement).disabled).toBe(false);
      }
    });

    it("disables the restart buttons for them, and not for a user who can ask for admin mode", async () => {
      const restartable = base({
        "GET /status": () =>
          json(status({ reboot: true, last: job({ kind: "upgrade", status: "done", post: post() }) })),
      });
      // until the history has loaded the page holds the restart back anyway: wait for it
      const loaded = async (api: ReturnType<typeof stubApi>) => {
        await waitFor(() =>
          expect(api.calls.some((c) => c.method === "GET" && c.path === "/jobs")).toBe(true),
        );
        await new Promise((r) => setTimeout(r, 30));
      };
      let api = stubApi(restartable);
      const cannot = renderExt(() => <Updates />, fakeSession({ admin: false, canAdmin: false }));
      await cannot.findByText("curl");
      await loaded(api);
      expect(restartButtons(cannot).map((b) => b.disabled)).toEqual([true, true]); // the banner's and the result's
      cleanup();
      api = stubApi(restartable);
      const can = renderExt(() => <Updates />, fakeSession({ admin: false, canAdmin: true }));
      await can.findByText("curl");
      await loaded(api);
      expect(restartButtons(can).map((b) => b.disabled)).toEqual([false, false]);
    });
  });

  it("drops the download size of the preview when the job starts, and shows none after it", async () => {
    stubApi(
      base({
        "POST /preview": () => json(plan()),
        "POST /jobs": () => json(job(), 202),
      }),
    );
    const view = renderExt(() => <Updates />);
    const download = () => view.queryByText("Download"); // the stat; the dialog says "Download: …"
    expect(await view.findByText("curl")).toBeTruthy();
    expect(download()).toBeNull(); // nothing previewed yet
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    await view.findByText("2 packages change.");
    await waitFor(() => expect(download()).toBeTruthy()); // the size of the preview
    fireEvent.click(view.getByRole("button", { name: "Install" }));
    expect(await view.findByText("Install updates")).toBeTruthy(); // the job panel
    expect(download()).toBeNull();
    lastSource()?.emit("end", JSON.stringify(job({ status: "done" })));
    await waitFor(() => expect(view.queryByText("Install updates")).toBeNull()); // the panel is gone
    expect(download()).toBeNull();
  });

  it("drops the download size of the preview when a job it did not start ends", async () => {
    let current: ReturnType<typeof job> | undefined;
    stubApi(
      base({
        "GET /status": () => json(status({ current })),
        "POST /preview": () => json(plan()),
      }),
    );
    const fake = fakeSession({ admin: true, canAdmin: true });
    const view = renderExt(() => <Updates />, fake);
    fireEvent.click(await view.findByRole("button", { name: "Install all" }));
    await view.findByText("2 packages change.");
    fireEvent.click(view.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(view.queryByText("Download")).toBeTruthy());
    // a job of someone else shows up in the status (admin mode changes, the page asks again)
    current = job({ kind: "refresh" });
    fake.setInfo((info) => (info ? { ...info, admin: false } : info));
    expect(await view.findByText("Check for updates")).toBeTruthy();
    lastSource()?.emit("end", JSON.stringify(job({ kind: "refresh", status: "done" })));
    await waitFor(() => expect(view.queryByText("Download")).toBeNull());
  });

  it("checks once, not twice, when the manual check has to ask for admin mode first", async () => {
    const old = new Date(Date.now() - 2 * 3600_000).toISOString();
    const api = stubApi(
      base({
        "GET /status": () => json(status({ checked: old })),
        "POST /refresh": () => json(job({ kind: "refresh" }), 202),
      }),
    );
    const fake = fakeSession({ admin: false, canAdmin: true });
    const session = {
      ...fake,
      api: {
        ...fake.api,
        requestAdmin: async () => {
          fake.setInfo((info) => (info ? { ...info, admin: true } : info));
          return true;
        },
      },
    };
    const view = renderExt(() => <Updates />, session);
    fireEvent.click(await view.findByRole("button", { name: "Check now" }));
    await waitFor(() => expect(api.posts("/refresh")).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 50)); // the automatic check, which would be the second
    expect(api.posts("/refresh")).toHaveLength(1);
  });
});
