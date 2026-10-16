import { describe, expect, it } from "vitest";
import de from "../public/locales/de.json";
import en from "../public/locales/en.json";

/** The dotted keys of the texts of a locale file ("form.policies.no"), the way the SDK flattens them. */
const keys = (tree: object, prefix = ""): string[] =>
  Object.entries(tree).flatMap(([key, value]) =>
    typeof value === "object" && value !== null ? keys(value, `${prefix}${key}.`) : [`${prefix}${key}`],
  );

describe("locales", () => {
  it("have the same keys in English and German", () => {
    const [english, german] = [keys(en), keys(de)];
    expect(english.length).toBeGreaterThan(0);
    expect(english.filter((key) => !german.includes(key))).toEqual([]); // missing in de.json
    expect(german.filter((key) => !english.includes(key))).toEqual([]); // missing in en.json
  });

  it("have the same placeholders in English and German", () => {
    const placeholders = (tree: object) =>
      Object.fromEntries(
        keys(tree).map((key) => {
          const text = key
            .split(".")
            .reduce((node, part) => (node as Record<string, unknown>)[part], tree as unknown);
          return [key, [...String(text).matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort()];
        }),
      );
    expect(placeholders(de)).toEqual(placeholders(en));
  });

  it("spell the German verb of the latest-stable blocker as one word", () => {
    expect(de.blocker.latest_stable_ahead).toContain("hinausführen");
    expect(de.blocker.latest_stable_ahead).not.toContain("hinaus führen");
  });

  it("say that lines with a tag stay as they are", () => {
    expect(en.warning.unmapped_repos).toContain("@edge");
    expect(de.warning.unmapped_repos).toContain("@edge");
  });
});
