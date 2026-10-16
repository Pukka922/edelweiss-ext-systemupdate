import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ReleaseDialog } from "./ReleaseDialog";
import { job, plan, releases } from "./test/fixtures";
import { json, renderExt, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const releasePlan = (target: string, over = {}) =>
  plan({ kind: "release", target, warnings: target === "edge" ? ["edge"] : ["notes_3_24"], ...over });

/** The text of the checklist row with this label: its mark (✓ ✗ –), the label and what follows it. */
const checkRow = (view: { getByText: (text: string) => HTMLElement }, label: string) =>
  view.getByText(label).closest("li")?.textContent ?? "";

describe("ReleaseDialog", () => {
  it("checks and starts the upgrade to the next release", async () => {
    const api = stubApi({
      "POST /preview": () => json(releasePlan("v3.24")),
      "POST /jobs": () => json(job({ kind: "release", target: "v3.24" }), 202),
    });
    const started = vi.fn();
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={started} />
    ));
    expect(view.getByText(/Alpine v3.24 \(3.24.2\), supported until/)).toBeTruthy();
    expect(view.getByRole("link", { name: "Release notes" }).getAttribute("href")).toContain("Alpine-3.24.0");
    const start = () => view.getByRole("button", { name: "Upgrade to v3.24" }) as HTMLButtonElement;
    expect(start().disabled).toBe(true); // not before the checks ran
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    await waitFor(() => expect(api.posts("/preview")[0]?.body).toEqual({ kind: "release", target: "v3.24" }));
    expect(await view.findByText(/Read the release notes of Alpine 3.24 first/)).toBeTruthy();
    expect(view.getByText("System installed to disk")).toBeTruthy();
    await waitFor(() => expect(start().disabled).toBe(false));
    fireEvent.click(start());
    await waitFor(() => expect(started).toHaveBeenCalled());
    expect(api.posts("/jobs")[0]?.body).toEqual({ kind: "release", planId: "0a1b2c3d4e5f6071" });
  });

  it("needs edge typed before it checks edge", async () => {
    const api = stubApi({ "POST /preview": () => json(releasePlan("edge")) });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByLabelText("edge"));
    expect(view.getByText(/There is no way back to a stable release/)).toBeTruthy();
    const check = () => view.getByRole("button", { name: "Run the checks" }) as HTMLButtonElement;
    expect(check().disabled).toBe(true);
    fireEvent.input(view.getByLabelText("Type edge to confirm"), { target: { value: "Edge" } });
    expect(check().disabled).toBe(true);
    fireEvent.input(view.getByLabelText("Type edge to confirm"), { target: { value: "edge" } });
    expect(check().disabled).toBe(false);
    fireEvent.click(check());
    await waitFor(() => expect(api.posts("/preview")[0]?.body).toEqual({ kind: "release", target: "edge" }));
  });

  it("shows what blocks the upgrade and does not start it", async () => {
    stubApi({ "POST /preview": () => json(releasePlan("v3.24", { blockers: ["usr_separate"] })) });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    expect(
      await view.findByText("/usr is a separate mount, which Alpine 3.24 no longer supports."),
    ).toBeTruthy();
    expect(view.getAllByText(/not checked/).length).toBe(4); // lock, mirror, space and simulation (P2–P4) did not run
    expect((view.getByRole("button", { name: "Upgrade to v3.24" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not claim the space and the simulation when a repository of the target is unavailable", async () => {
    stubApi({
      "POST /preview": () =>
        json(
          releasePlan("v3.24", { blockers: ["repos_unavailable"], messages: ["ERROR: community: IO ERROR"] }),
        ),
    });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    expect(await view.findByText("apk could not use a repository of the target:")).toBeTruthy();
    expect(checkRow(view, "Repositories of the target reachable")).toContain("✗");
    expect(checkRow(view, "No other apk running")).toContain("✓"); // the lock was free: the update went on
    expect(checkRow(view, "Free space")).toContain("not checked");
    expect(checkRow(view, "Simulation")).toContain("not checked");
    expect(view.getAllByText(/not checked/).length).toBe(2);
  });

  it("names who holds the apk lock and does not claim the checks after it", async () => {
    stubApi({
      "POST /preview": () =>
        json(releasePlan("v3.24", { blockers: ["apk_busy"], lockHolder: { pid: 1234, name: "apk" } })),
    });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    expect(
      await view.findByText("Another apk runs (apk, pid 1234). Try again when it has finished."),
    ).toBeTruthy();
    expect(checkRow(view, "No other apk running")).toContain("✗");
    expect(checkRow(view, "Repositories of the target reachable")).toContain("not checked");
    expect(checkRow(view, "Free space")).toContain("not checked");
    expect(checkRow(view, "Simulation")).toContain("not checked");
    expect(checkRow(view, "System installed to disk")).toContain("✓"); // the early checks did run
  });

  it("links the release notes only over https", () => {
    stubApi({});
    const bad = releases({
      targets: [{ branch: "v3.24", eol: "2028-06-01", latest: "3.24.2", notes: "javascript:alert(1)" }],
    });
    const view = renderExt(() => <ReleaseDialog releases={bad} onClose={() => {}} onStarted={() => {}} />);
    expect(view.getByText(/Alpine v3.24 \(3.24.2\), supported until/)).toBeTruthy();
    expect(view.queryByRole("link", { name: "Release notes" })).toBeNull();
    expect(view.container.querySelector("a[href^='javascript:']")).toBeNull();
  });

  it("locks the choice while a check runs", async () => {
    let answer: (r: Response) => void = () => {};
    stubApi({ "POST /preview": () => new Promise<Response>((resolve) => (answer = resolve)) });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByLabelText("edge"));
    fireEvent.input(view.getByLabelText("Type edge to confirm"), { target: { value: "edge" } });
    const locked = () => [
      ...view.getAllByRole("radio").map((r) => (r as HTMLInputElement).disabled),
      (view.getByLabelText("Type edge to confirm") as HTMLInputElement).disabled,
    ];
    expect(locked()).toEqual([false, false, false]);
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    await waitFor(() => expect(locked()).toEqual([true, true, true]));
    answer(json(releasePlan("edge")));
    await waitFor(() => expect(locked()).toEqual([false, false, false]));
  });

  it("says when there is no target or no release list", () => {
    stubApi({});
    const none = renderExt(() => (
      <ReleaseDialog releases={releases({ targets: [] })} onClose={() => {}} onStarted={() => {}} />
    ));
    expect(none.getByText("There is no newer release to upgrade to.")).toBeTruthy();
    cleanup();
    const nolist = renderExt(() => (
      <ReleaseDialog
        releases={releases({ available: false, targets: [] })}
        onClose={() => {}}
        onStarted={() => {}}
      />
    ));
    expect(nolist.getByText(/release list is not available/)).toBeTruthy();
  });

  it("sends the typed confirmation of edge with the job", async () => {
    const api = stubApi({
      "POST /preview": () => json(releasePlan("edge")),
      "POST /jobs": () => json(job({ kind: "release", target: "edge" }), 202),
    });
    const started = vi.fn();
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={started} />
    ));
    fireEvent.click(view.getByLabelText("edge"));
    fireEvent.input(view.getByLabelText("Type edge to confirm"), { target: { value: "edge" } });
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    const start = () => view.getByRole("button", { name: "Upgrade to edge" }) as HTMLButtonElement;
    await waitFor(() => expect(start().disabled).toBe(false));
    fireEvent.click(start());
    await waitFor(() => expect(started).toHaveBeenCalled());
    expect(api.posts("/jobs")[0]?.body).toEqual({
      kind: "release",
      planId: "0a1b2c3d4e5f6071",
      confirm: "edge",
    });
  });

  it("names the latest-stable lines that would carry the upgrade past the target", async () => {
    stubApi({
      "POST /preview": () => json(releasePlan("v3.24", { blockers: ["latest_stable_ahead"] })),
    });
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    fireEvent.click(view.getByRole("button", { name: "Run the checks" }));
    expect(
      await view.findByText(
        "Lines with latest-stable would carry the upgrade past v3.24: point them to a branch first.",
      ),
    ).toBeTruthy();
    expect(checkRow(view, "Repositories")).toContain("✗"); // in the row of the repositories, not as an unknown blocker
    expect(view.queryByText(/The repositories do not all point/)).toBeNull();
    expect(checkRow(view, "No separate /usr")).toContain("✓");
    expect((view.getByRole("button", { name: "Upgrade to v3.24" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("explains that the next release has ended instead of offering a jump over it", () => {
    stubApi({});
    const view = renderExt(() => (
      <ReleaseDialog
        releases={releases({ targets: [{ branch: "edge", edge: true }] })} // v3.24 is the newest, v3.23 the host
        onClose={() => {}}
        onStarted={() => {}}
      />
    ));
    expect(
      view.getByText(
        "The next release after v3.23 has reached its end of life: this page offers no release upgrade from here. Upgrade one branch at a time by hand (see the Alpine release notes).",
      ),
    ).toBeTruthy();
    expect(view.getAllByRole("radio")).toHaveLength(1); // edge only
    expect(view.getByLabelText("edge")).toBeTruthy();
    expect(view.queryByText("Next release")).toBeNull(); // no empty group of stable releases
  });

  it("does not claim an ended release when the host already runs the newest one", () => {
    stubApi({});
    const view = renderExt(() => (
      <ReleaseDialog
        releases={releases({ current: "v3.24", latest: "v3.24", targets: [{ branch: "edge", edge: true }] })}
        onClose={() => {}}
        onStarted={() => {}}
      />
    ));
    expect(view.queryByText(/has reached its end of life/)).toBeNull();
    expect(view.getByLabelText("edge")).toBeTruthy();
  });

  it("does not claim an ended release when there is a stable target", () => {
    stubApi({});
    const view = renderExt(() => (
      <ReleaseDialog releases={releases()} onClose={() => {}} onStarted={() => {}} />
    ));
    expect(view.queryByText(/has reached its end of life/)).toBeNull();
  });
});
