import { ApiError, type Translator } from "@edelweiss/sdk";

/** The job a 409 busy names (details.job of the error), as far as its kind and target are there. */
function runningJob(err: ApiError): { kind: string; target: string } | undefined {
  const job = err.details.job;
  if (typeof job !== "object" || job === null) return undefined;
  const { kind, target } = job as { kind?: unknown; target?: unknown };
  return typeof kind === "string" && kind !== ""
    ? { kind, target: typeof target === "string" ? target : "" }
    : undefined;
}

/**
 * A text for an error of the backend: the translation of its code, else its message. Another job that runs (409 busy)
 * is named when the answer says which: by the translated name of its kind, as the history shows it.
 */
export function errorText(err: unknown, t: Translator["t"]): string {
  if (err instanceof ApiError) {
    const job = err.code === "busy" ? runningJob(err) : undefined;
    if (job) {
      const kindKey = `job.kind.${job.kind}`;
      const name = t(kindKey, { target: job.target });
      return t("errors.busyJob", { job: name === kindKey ? job.kind : name });
    }
    const key = `errors.${err.code}`;
    const text = t(key, { message: err.message });
    return text === key ? err.message : text;
  }
  return err instanceof Error ? err.message : String(err);
}
