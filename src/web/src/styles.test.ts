import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Browser-found regression (DEMO-51 smoke test): `.file-label input { display: none }` made
// "Upload file" unreachable with the keyboard. jsdom does not apply stylesheets, so the
// rule is checked in the CSS source.
describe("styles.css keyboard accessibility", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
  const rule = (sel: string) => {
    const m = css.match(new RegExp(sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\{([^}]*)\\}"));
    return m ? m[1] : "";
  };

  it("keeps the upload input focusable (not display:none / visibility:hidden)", () => {
    const body = rule(".file-label input");
    expect(body).not.toBe("");
    expect(body).not.toMatch(/display\s*:\s*none/);
    expect(body).not.toMatch(/visibility\s*:\s*hidden/);
  });

  it("does not break normal words mid-word in the compare panel (long ids may wrap)", () => {
    const body = rule(".compare-env");
    expect(body).not.toMatch(/word-break\s*:\s*break-all/);
    expect(body).toMatch(/overflow-wrap\s*:\s*anywhere/);
  });

  it("shows a focus ring on the label when its input has focus", () => {
    expect(rule(".file-label:focus-within")).toMatch(/outline\s*:\s*\d+px solid/);
  });

  const luminance = (hex: string) => {
    const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)));
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const ratio = (a: string, b: string) => {
    const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
  };
  const color = (sel: string) => (rule(sel).match(/(?:^|[;\s])color\s*:\s*(#[0-9a-fA-F]{6})/) ?? [])[1] ?? "";

  it("keeps secondary text above 4.5:1 on every background it is shown on", () => {
    const muted = color(".muted");
    expect(muted).not.toBe("");
    for (const bg of ["#ffffff", "#f8fbff", "#f3f6fb", "#e8eff7"]) {
      expect(ratio(muted, bg)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("keeps the timeline time above 4.5:1 on a selected row", () => {
    const time = color(".event-row .time");
    expect(time).not.toBe("");
    for (const bg of ["#ffffff", "#f3f6fb", "#e8eff7"]) {
      expect(ratio(time, bg)).toBeGreaterThanOrEqual(4.5);
    }
  });
});
