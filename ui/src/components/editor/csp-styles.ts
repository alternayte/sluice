import { StyleModule } from "style-mod";

// The server sends `Content-Security-Policy: default-src 'self'` (SI-12). That policy
// blocks the <style> elements that CodeMirror adds through style-mod, so the editor had no
// base styles and the lint markers had no size. A constructed style sheet in
// adoptedStyleSheets is not an inline style element, so the policy does not block it
// (DI-32). This module replaces StyleModule.mount with a version that keeps the module
// order of style-mod and writes all rules of a root into one constructed sheet.

type Root = Document | ShadowRoot;
type SheetSet = { sheet: CSSStyleSheet; modules: StyleModule[] };

const sets = new WeakMap<Root, SheetSet>();

function setFor(root: Root): SheetSet {
  let set = sets.get(root);
  if (!set) {
    set = { sheet: new CSSStyleSheet(), modules: [] };
    root.adoptedStyleSheets = [...root.adoptedStyleSheets, set.sheet];
    sets.set(root, set);
  }
  return set;
}

function supported(): boolean {
  return (
    typeof CSSStyleSheet !== "undefined" &&
    typeof Document !== "undefined" &&
    "adoptedStyleSheets" in Document.prototype
  );
}

if (supported()) {
  StyleModule.mount = (root, modules) => {
    const set = setFor(root as Root);
    const list = Array.isArray(modules) ? (modules as readonly StyleModule[]) : [modules as StyleModule];
    let j = 0;
    let changed = false;
    for (const mod of list) {
      let index = set.modules.indexOf(mod);
      if (index > -1 && index < j) {
        // An ordering conflict: the module moves to the current position, as in style-mod.
        set.modules.splice(index, 1);
        j--;
        index = -1;
      }
      if (index === -1) {
        set.modules.splice(j++, 0, mod);
        changed = true;
      } else {
        j = index + 1;
      }
    }
    if (changed) set.sheet.replaceSync(set.modules.map((m) => m.getRules()).join("\n"));
  };
}
