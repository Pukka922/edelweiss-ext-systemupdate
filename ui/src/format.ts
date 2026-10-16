const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

/** The size of n bytes (its absolute value) in IEC units, with at most one decimal: "1.5 KiB". */
export function formatBytes(n: number, locale: string): string {
  let v = Math.abs(n);
  let unit = 0;
  while (v >= 1024 && unit < UNITS.length - 1) {
    v /= 1024;
    unit++;
  }
  const num = new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(v);
  return `${num} ${UNITS[unit]}`;
}

/** How long ago iso was, seen at now, in words of the locale: "5 minutes ago". */
export function formatAgo(iso: string, now: number, locale: string): string {
  const s = Math.round((Date.parse(iso) - now) / 1000);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  const abs = Math.abs(s);
  if (abs < 60) return rtf.format(s, "second");
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

/** A date of releases.json ("2026-05-01") or a time stamp, as a date of the locale. */
export function formatDate(iso: string, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(iso));
}

/** A time stamp as a short date and time of the locale. */
export function formatTime(iso: string, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "short", timeStyle: "short" }).format(new Date(iso));
}

/** A duration as m:ss or h:mm:ss. */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/** Whether a branch is v3.24 or newer (edge included): the release with the merged /usr and the GRUB step. */
export function fromV324(branch: string | undefined): boolean {
  if (branch === "edge") return true;
  const m = /^v(\d+)\.(\d+)$/.exec(branch ?? "");
  if (!m) return false;
  const [major, minor] = [Number(m[1]), Number(m[2])];
  return major > 3 || (major === 3 && minor >= 24);
}

/** Whether the stable branch a (vX.Y) is newer than the stable branch b; false when either is not a stable branch. */
export function branchNewer(a: string | undefined, b: string | undefined): boolean {
  const pa = /^v(\d+)\.(\d+)$/.exec(a ?? "");
  const pb = /^v(\d+)\.(\d+)$/.exec(b ?? "");
  if (!pa || !pb) return false;
  const [majorA, minorA, majorB, minorB] = [Number(pa[1]), Number(pa[2]), Number(pb[1]), Number(pb[2])];
  return majorA > majorB || (majorA === majorB && minorA > minorB);
}

/** The URL if it is an https: URL, else undefined: URLs of the server (advisories, release notes) go into a link only so. */
export function safeHttps(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    return u.protocol === "https:" ? u.href : undefined;
  } catch {
    return undefined;
  }
}
