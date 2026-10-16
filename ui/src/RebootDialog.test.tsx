import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RebootDialog } from "./RebootDialog";
import { apiError, json, renderExt, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RebootDialog", () => {
  it("restarts through the core and waits until the host is back", async () => {
    let health = 0;
    const api = stubApi({
      "POST /api/system/reboot": () => json({ action: "reboot" }, 202),
      // up, down, up again
      "GET /api/health": () =>
        health++ === 1 ? new Response(null, { status: 502 }) : json({ status: "ok" }),
    });
    const back = vi.fn();
    const view = renderExt(() => <RebootDialog onClose={() => {}} pollMs={1} onBack={back} />);
    expect(view.getByText("Restart alpine now? All connections will be closed.")).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Restart" }));
    expect(await view.findByText("alpine is restarting …")).toBeTruthy();
    await waitFor(() => expect(back).toHaveBeenCalled());
    const reboot = api.posts("/api/system/reboot")[0];
    expect(reboot?.headers["X-Csrf-Token"]).toBe("csrf-1");
  });

  it("reports a refused restart", async () => {
    stubApi({ "POST /api/system/reboot": () => apiError(403, "admin_required", "admin mode needed") });
    const closed = vi.fn();
    const view = renderExt(() => <RebootDialog onClose={closed} pollMs={1} />);
    fireEvent.click(view.getByRole("button", { name: "Restart" }));
    await waitFor(() => expect(closed).toHaveBeenCalled());
    expect(view.queryByText("alpine is restarting …")).toBeNull();
  });

  it("offers to reload the page when the host never went down", async () => {
    stubApi({
      "POST /api/system/reboot": () => json({ action: "reboot" }, 202),
      "GET /api/health": () => json({ status: "ok" }), // always up
    });
    const reload = vi.fn();
    const back = vi.fn();
    const view = renderExt(() => (
      <RebootDialog onClose={() => {}} pollMs={1} stillUpMs={5} onBack={back} onReload={reload} />
    ));
    fireEvent.click(view.getByRole("button", { name: "Restart" }));
    expect(await view.findByText("alpine is restarting …")).toBeTruthy();
    expect(await view.findByText(/The host still answers/)).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(back).not.toHaveBeenCalled();
  });
});
