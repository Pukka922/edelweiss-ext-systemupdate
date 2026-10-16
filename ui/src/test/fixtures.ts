import type { Job, Plan, Post, Releases, Status, Updates } from "../api";

const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000).toISOString();

/** The post-checks of a job that changed the kernel: a restart is needed, the boot files are fresh. */
export const post = (over: Partial<Post> = {}): Post => ({
  reboot: true,
  bootOk: true,
  kernelChanged: true,
  apkNew: [],
  stale: [],
  removed: [],
  grub: false,
  grubEfi: false,
  updateKernel: false,
  ...over,
});

export const status = (over: Partial<Status> = {}): Status => ({
  release: "3.23.6",
  branch: "v3.23",
  arch: "x86_64",
  mode: "sys",
  reboot: false,
  bootStale: false,
  checked: minutesAgo(5),
  banners: [
    { kind: "repo_eol", branch: "v3.23", repo: "community", date: "2026-05-01" },
    { kind: "new_release", branch: "v3.24", date: "2028-06-01" },
  ],
  ...over,
});

export const updates = (over: Partial<Updates> = {}): Updates => ({
  checked: minutesAgo(5),
  securityData: true,
  items: [
    {
      name: "curl",
      old: "8.16.0-r0",
      new: "8.16.0-r1",
      origin: "curl",
      security: [{ id: "CVE-2026-2222", link: "https://security.alpinelinux.org/vuln/CVE-2026-2222" }],
      reboot: false,
    },
    {
      name: "linux-lts",
      old: "6.12.1-r0",
      new: "6.12.2-r0",
      origin: "linux-lts",
      security: [],
      reboot: true,
    },
  ],
  ...over,
});

export const releases = (over: Partial<Releases> = {}): Releases => ({
  available: true,
  current: "v3.23",
  latest: "v3.24",
  targets: [
    {
      branch: "v3.24",
      eol: "2028-06-01",
      latest: "3.24.2",
      notes: "https://alpinelinux.org/posts/Alpine-3.24.0-released.html",
    },
    { branch: "edge", edge: true },
  ],
  ...over,
});

export const plan = (over: Partial<Plan> = {}): Plan => ({
  id: "0a1b2c3d4e5f6071",
  kind: "upgrade",
  created: new Date().toISOString(),
  simulation: {
    changes: [
      { verb: "Upgrading", name: "curl", old: "8.16.0-r0", new: "8.16.0-r1" },
      { verb: "Purging", name: "gtk+2.0", old: "2.24.33-r9", new: "" },
    ],
    download: 386 * 2 ** 20,
    additional: 41 * 2 ** 20,
    sizesKnown: true,
    ok: true,
    errors: 0,
  },
  held: [],
  messages: [],
  warnings: ["removals"],
  blockers: [],
  ...over,
});

export const job = (over: Partial<Job> = {}): Job => ({
  id: "20261013T120000.000-0a1b2c3d",
  kind: "upgrade",
  user: "alice",
  status: "running",
  phase: "upgrade",
  steps: [],
  created: minutesAgo(1),
  started: minutesAgo(1),
  ...over,
});
