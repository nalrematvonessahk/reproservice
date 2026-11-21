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
});
