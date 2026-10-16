import { type JSX, onCleanup, onMount } from "solid-js";

/** A modal dialog like the SDK's ConfirmDialog, with content of its own. Escape and the backdrop close it unless busy. */
export function Modal(props: {
  title: string;
  busy?: boolean;
  wide?: boolean;
  onClose: () => void;
  children: JSX.Element;
  actions: JSX.Element;
}) {
  const close = () => {
    if (!props.busy) props.onClose();
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape") close();
  };
  onMount(() => document.addEventListener("keydown", onKeyDown));
  onCleanup(() => document.removeEventListener("keydown", onKeyDown));
  return (
    <div class="modal modal-open" role="dialog" aria-modal="true" aria-label={props.title}>
      <div class={props.wide ? "modal-box max-w-3xl" : "modal-box"}>
        <h3 class="text-lg font-bold">{props.title}</h3>
        <div class="flex flex-col gap-3 py-4">{props.children}</div>
        <div class="modal-action">{props.actions}</div>
      </div>
      <div class="modal-backdrop" aria-hidden="true" onClick={close} />
    </div>
  );
}
