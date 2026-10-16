/** The shapes of the extension's API (internal/api and internal/jobs). */

export type Mode = "sys" | "diskless" | "data";
export type JobKind = "refresh" | "upgrade" | "release" | "resume";
export type JobStatus =
  | "queued"
  | "running"
  | "done"
  | "partial"
  | "failed"
  | "failed_clean"
  | "done_not_persisted"
  | "interrupted";

export interface Banner {
  kind:
    | "new_release"
    | "next_release_ended"
    | "branch_eol"
    | "branch_eol_soon"
    | "repo_eol"
    | "repo_eol_soon";
  /** The installed branch; for new_release and next_release_ended the next stable branch. */
  branch: string;
  repo?: string;
  date?: string;
}

export interface Resume {
  target: string;
  reason: "mismatch" | "partial" | "interrupted" | "failed";
}

export interface Proc {
  pid: number;
  name: string;
  service?: string;
}

/** What the page shows after a job. kernelChanged and bootOk are about this one job; the restart gate is Status.bootStale. */
export interface Post {
  reboot: boolean;
  /** The boot files were fresh when the job ended (true when it changed no kernel). */
  bootOk: boolean;
  /** This job's own output changed a kernel package. */
  kernelChanged: boolean;
  apkNew: string[];
  stale: Proc[];
  removed: string[];
  grub: boolean;
  grubEfi: boolean;
  updateKernel: boolean;
}

export interface Step {
  name: string;
  exit: number;
  errors: number;
  started: string;
  ended: string;
}

export interface Job {
  id: string;
  kind: JobKind;
  target?: string;
  user: string;
  status: JobStatus;
  phase?: string;
  steps: Step[];
  created: string;
  started?: string;
  ended?: string;
  apkPid?: number;
  apkAlive?: boolean;
  waiting?: boolean;
  lockHolder?: Proc;
  reposSwitched?: boolean;
  progress?: { cur: number; max: number; stage?: string };
  current?: string;
  post?: Post;
  error?: string;
}

export interface Status {
  release: string;
  branch: string;
  arch: string;
  mode: Mode;
  reboot: boolean;
  /** A restart is pending and the boot files of the installed kernel are missing or older than the kernel: live. */
  bootStale: boolean;
  checked?: string;
  banners: Banner[];
  resume?: Resume;
  current?: Job;
  last?: Job;
}

export interface Advisory {
  id: string;
  link?: string;
}

export interface Update {
  name: string;
  old: string;
  new: string;
  origin: string;
  security: Advisory[];
  reboot: boolean;
}

export interface Updates {
  checked?: string;
  securityData: boolean;
  items: Update[];
}

export interface Target {
  branch: string;
  eol?: string;
  latest?: string;
  notes?: string;
  edge?: boolean;
}

export interface Releases {
  available: boolean;
  fetched?: string;
  current: string;
  latest?: string;
  targets: Target[];
}

export interface Change {
  verb: string;
  name: string;
  old?: string; // omitempty in Go: a missing version is shown as nothing
  new?: string;
}

export interface Simulation {
  changes: Change[] | null; // Go's nil slice
  download: number;
  additional: number;
  sizesKnown: boolean;
  ok: boolean;
  errors: number;
}

export interface Plan {
  id: string;
  kind: "upgrade" | "release";
  target?: string;
  created: string;
  simulation: Simulation;
  repos?: { changed: number; unmapped: string[]; latestStable: number };
  held: string[];
  messages: string[];
  warnings: string[];
  blockers: string[];
  lockHolder?: Proc;
}

export interface LogEvent {
  lines: string[];
  offset: number;
}

export const isFinished = (s: JobStatus) => s !== "queued" && s !== "running";

/** The changes of a simulation that are no mere downloads, and among them the removals and downgrades. */
export function changesOf(sim: Simulation) {
  const all = (sim.changes ?? []).filter((c) => c.verb !== "Downloading");
  return {
    all,
    removed: all.filter((c) => c.verb === "Purging"),
    downgraded: all.filter((c) => c.verb === "Downgrading"),
  };
}
