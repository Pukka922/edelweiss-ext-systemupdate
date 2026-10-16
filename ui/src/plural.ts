import type { Translator } from "@edelweiss/sdk";

/**
 * The text of key for count in the plural form of the language. The SDK's t has no plurals, so a text with forms has the
 * nested keys key.one and key.other, and Intl.PluralRules picks (a language with more forms would add keys; a form
 * without a text falls back to other). {{ count }} is count, or params.count where the number is shown formatted.
 */
export function tPlural(
  i18n: Pick<Translator, "t" | "locale">,
  key: string,
  count: number,
  params: Record<string, string | number> = {},
): string {
  const form = `${key}.${new Intl.PluralRules(i18n.locale()).select(count)}`;
  const text = i18n.t(form, { count, ...params });
  return text === form ? i18n.t(`${key}.other`, { count, ...params }) : text;
}
