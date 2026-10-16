import { cleanup, fireEvent } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Job } from "./api";
import { Result } from "./Result";
import { job, post } from "./test/fixtures";
import { renderExt, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function mount(j: Job, reboot = true, blocked = false) {
  stubApi({});
  const onReboot = vi.fn();
  const view = renderExt(() => {
    const [grub, setGrub] = createSignal(false);
    return (
      <Result
        job={j}
        arch="x86_64"
        reboot={reboot}
        rebootBlocked={blocked}
        grubDone={grub()}
        onGrubDone={setGrub}
        onReboot={onReboot}
      />
    );
  });
  return {
    ...view,
    onReboot,
    restart: () => view.getByRole("button", { name: "Restart …" }) as HTMLButtonElement,
  };
}

describe("Result", () => {
  it("offers the restart after a new kernel", () => {
    const view = mount(job({ status: "done", post: post() }));
    expect(view.getByText("A restart is needed to use the new kernel.")).toBeTruthy();
    fireEvent.click(view.restart());
    expect(view.onReboot).toHaveBeenCalled();
  });

  it("keeps the restart button disabled while the page blocks the restart", () => {
    const view = mount(job({ status: "done", post: post() }), true, true);
    expect(view.restart().disabled).toBe(true);
    fireEvent.click(view.restart());
    expect(view.onReboot).not.toHaveBeenCalled();
  });

  it("says not to restart when the boot files are stale", () => {
    const view = mount(job({ status: "done", post: post({ bootOk: false }) }));
    expect(view.getByText(/Do NOT restart: the boot files/)).toBeTruthy();
    expect(view.queryByRole("button", { name: "Restart …" })).toBeNull();
  });

  it("waits for grub-install after a release upgrade to 3.24", () => {
    const view = mount(
      job({ kind: "release", target: "v3.24", status: "done", post: post({ grub: true, grubEfi: true }) }),
    );
    expect(view.getByText("grub-install --target=x86_64-efi --efi-directory=/boot/efi")).toBeTruthy();
    expect(view.restart().disabled).toBe(true);
    fireEvent.click(view.getByLabelText("I have run grub-install"));
    expect(view.restart().disabled).toBe(false);
  });

  it("says not to restart after a partial release upgrade", () => {
    const view = mount(
      job({ kind: "release", target: "v3.24", status: "partial", reposSwitched: true, post: post() }),
    );
    expect(view.getByText("The release upgrade stopped halfway. Do NOT restart: resume it.")).toBeTruthy();
    expect(view.queryByRole("button", { name: "Restart …" })).toBeNull();
  });

  it("lists new configuration files, stale processes and removed packages", () => {
    const view = mount(
      job({
        status: "done",
        post: post({
          reboot: false,
          kernelChanged: false,
          apkNew: ["/etc/ssh/sshd_config.apk-new"],
          stale: [{ pid: 812, name: "sshd", service: "sshd" }],
          removed: ["gtk+2.0"],
        }),
      }),
      false,
    );
    expect(view.getByText("/etc/ssh/sshd_config.apk-new")).toBeTruthy();
    expect(view.getByText("sshd (pid 812, service sshd)")).toBeTruthy();
    expect(view.getByText("gtk+2.0")).toBeTruthy();
    expect(view.queryByText("A restart is needed to use the new kernel.")).toBeNull();
  });

  it("shows a failed diskless commit in red", () => {
    const view = mount(job({ status: "done_not_persisted", post: post({ reboot: false }) }), false);
    expect(view.getByRole("alert").textContent).toContain("lbu commit failed");
  });

  it("words the result of a check that ended partial or failed as a check, not as an upgrade", () => {
    // e.g. no network: apk update fails, or some repositories could not be fetched
    const partial = mount(job({ kind: "refresh", status: "partial" }), false);
    expect(partial.getByRole("alert").textContent).toBe(
      "Some repositories could not be checked: the list may be incomplete.",
    );
    cleanup();
    const failed = mount(job({ kind: "refresh", status: "failed" }), false);
    expect(failed.getByRole("alert").textContent).toBe("The check failed.");
    cleanup();
    // the same statuses of an upgrade keep their own words
    expect(
      mount(job({ kind: "upgrade", status: "partial" }), false).getByRole("alert").textContent,
    ).toContain("The job finished with errors");
    cleanup();
    expect(mount(job({ kind: "upgrade", status: "failed" }), false).getByRole("alert").textContent).toBe(
      "The job failed.",
    );
  });
});
