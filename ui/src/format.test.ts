import { describe, expect, it } from "vitest";
import { branchNewer, formatAgo, formatBytes, formatDuration, fromV324, safeHttps } from "./format";

describe("format", () => {
  it("formats bytes in IEC units", () => {
    expect(formatBytes(0, "en")).toBe("0 B");
    expect(formatBytes(1536, "en")).toBe("1.5 KiB");
    expect(formatBytes(386 * 2 ** 20, "en")).toBe("386 MiB");
    expect(formatBytes(1536, "de")).toBe("1,5 KiB");
    expect(formatBytes(-2048, "en")).toBe("2 KiB");
  });

  it("says how long ago", () => {
    const now = Date.parse("2026-10-13T12:00:00Z");
    expect(formatAgo("2026-10-13T11:55:00Z", now, "en")).toBe("5 minutes ago");
    expect(formatAgo("2026-10-13T11:55:00Z", now, "de")).toBe("vor 5 Minuten");
    expect(formatAgo("2026-10-13T09:00:00Z", now, "en")).toBe("3 hours ago");
  });

  it("formats durations", () => {
    expect(formatDuration(65_000)).toBe("1:05");
    expect(formatDuration(3_723_000)).toBe("1:02:03");
  });

  it("knows the branches from 3.24 on", () => {
    expect(fromV324("v3.23")).toBe(false);
    expect(fromV324("v3.24")).toBe(true);
    expect(fromV324("v3.100")).toBe(true);
    expect(fromV324("edge")).toBe(true);
    expect(fromV324(undefined)).toBe(false);
  });

  it("tells whether a branch is newer than another", () => {
    expect(branchNewer("v3.24", "v3.23")).toBe(true);
    expect(branchNewer("v3.100", "v3.99")).toBe(true);
    expect(branchNewer("v4.0", "v3.23")).toBe(true);
    expect(branchNewer("v3.23", "v3.23")).toBe(false);
    expect(branchNewer("v3.22", "v3.23")).toBe(false);
    expect(branchNewer(undefined, "v3.23")).toBe(false);
    expect(branchNewer("v3.24", undefined)).toBe(false);
    expect(branchNewer("edge", "v3.23")).toBe(false); // not a stable branch
    expect(branchNewer("v3.24", "edge")).toBe(false);
  });

  it("lets only https URLs into a link", () => {
    expect(safeHttps("https://security.alpinelinux.org/vuln/CVE-2026-2222")).toBe(
      "https://security.alpinelinux.org/vuln/CVE-2026-2222",
    );
    expect(safeHttps("javascript:alert(1)")).toBeUndefined();
    expect(safeHttps(" JavaScript:alert(1)")).toBeUndefined();
    expect(safeHttps("data:text/html,<script>alert(1)</script>")).toBeUndefined();
    expect(safeHttps("http://alpinelinux.org/posts/x.html")).toBeUndefined();
    expect(safeHttps("//alpinelinux.org/posts/x.html")).toBeUndefined();
    expect(safeHttps("/relative")).toBeUndefined();
    expect(safeHttps("")).toBeUndefined();
    expect(safeHttps(undefined)).toBeUndefined();
  });
});
