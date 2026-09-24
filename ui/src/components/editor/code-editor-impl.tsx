import "./csp-styles";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { javascript } from "@codemirror/lang-javascript";
import { python } from "@codemirror/lang-python";
import { sql } from "@codemirror/lang-sql";
import { yaml } from "@codemirror/lang-yaml";
import {
  HighlightStyle,
  StreamLanguage,
  syntaxHighlighting,
  bracketMatching,
  indentOnInput,
} from "@codemirror/language";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { linter, lintGutter } from "@codemirror/lint";
import { Compartment, EditorState, Prec, type Extension } from "@codemirror/state";
import {
  EditorView,
  drawSelection,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { useEffect, useRef, useSyncExternalStore } from "react";
import { issuesToDiagnostics, type Issue } from "./diagnostics";
import { languageForPath, type LanguageId } from "./languages";

export type CodeEditorProps = {
  value: string;
  path: string;
  label: string;
  readOnly?: boolean;
  onChange?: (value: string) => void;
  /** validate returns the server issues for the content. When set, the editor lints the content. */
  validate?: (content: string) => Promise<Issue[]>;
  onIssues?: (issues: Issue[]) => void;
  /** onSave runs on Mod-s. The browser save dialog never opens from the editor. */
  onSave?: () => void;
  /** onRun runs on Mod-Enter. Without it, Mod-Enter keeps its editing command. */
  onRun?: () => void;
};

function languageExtension(id: LanguageId): Extension {
  switch (id) {
    case "yaml":
      return yaml();
    case "python":
      return python();
    case "shell":
      return StreamLanguage.define(shell);
    case "javascript":
      return javascript();
    case "jsx":
      return javascript({ jsx: true });
    case "typescript":
      return javascript({ typescript: true });
    case "tsx":
      return javascript({ typescript: true, jsx: true });
    case "sql":
      return sql();
    default:
      return [];
  }
}

function subscribeHtmlClass(cb: () => void) {
  const obs = new MutationObserver(cb);
  obs.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  return () => obs.disconnect();
}

/** useHtmlDark reports whether the app theme is dark (the dark class on the html element). */
function useHtmlDark(): boolean {
  return useSyncExternalStore(
    subscribeHtmlClass,
    () => document.documentElement.classList.contains("dark"),
    () => false,
  );
}

// Syntax colors in the manner of the Xcode default themes. Each color reaches 4.5:1 on the
// panel and on the active line in its theme (REQ-UI-011).
const palette = {
  light: {
    keyword: "#9b2393",
    string: "#c41a16",
    number: "#1c00cf",
    comment: "#5d6c79",
    type: "#1c6e77",
    property: "#6c36a9",
    fn: "#0f68a0",
    attribute: "#815f03",
  },
  dark: {
    keyword: "#ff7ab2",
    string: "#ff8170",
    number: "#d9c97c",
    comment: "#8a97a3",
    type: "#5dd8ff",
    property: "#4eb0cc",
    fn: "#67b7a4",
    attribute: "#cc9768",
  },
};

function highlight(c: (typeof palette)["light"]) {
  return HighlightStyle.define([
    { tag: [t.keyword, t.controlKeyword, t.modifier, t.operatorKeyword, t.bool, t.null, t.self], color: c.keyword },
    { tag: [t.string, t.special(t.string), t.character, t.regexp, t.escape], color: c.string },
    { tag: [t.number, t.integer, t.float, t.atom], color: c.number },
    { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: c.comment },
    { tag: [t.typeName, t.className, t.namespace, t.standard(t.typeName)], color: c.type },
    {
      tag: [t.propertyName, t.definition(t.propertyName), t.attributeName, t.special(t.variableName)],
      color: c.property,
    },
    {
      tag: [
        t.function(t.variableName),
        t.function(t.propertyName),
        t.definition(t.variableName),
        t.standard(t.variableName),
      ],
      color: c.fn,
    },
    { tag: [t.meta, t.annotation, t.labelName, t.attributeValue], color: c.attribute },
    { tag: t.invalid, color: c.string, textDecoration: "underline wavy" },
  ]);
}

const lightHighlight = highlight(palette.light);
const darkHighlight = highlight(palette.dark);

/** The editor chrome takes the panel tokens, so it follows the app theme without its own colors. */
function editorTheme(dark: boolean): Extension {
  const theme = EditorView.theme(
    {
      "&": { backgroundColor: "var(--panel)", color: "var(--foreground)", fontSize: "12.5px", height: "100%" },
      "&.cm-focused": { outline: "none" },
      ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.65" },
      ".cm-content": { caretColor: "var(--accent)", padding: "6px 0" },
      ".cm-line": { padding: "0 12px 0 6px" },
      ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--accent)", borderLeftWidth: "2px" },
      ".cm-gutters": {
        backgroundColor: "var(--panel)",
        color: "var(--muted-foreground)",
        border: "none",
      },
      ".cm-lineNumbers .cm-gutterElement": { padding: "0 6px 0 14px", minWidth: "36px" },
      ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--muted) 70%, transparent)" },
      ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--foreground)" },
      ".cm-selectionBackground": { backgroundColor: "color-mix(in srgb, var(--accent) 16%, transparent)" },
      "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground": {
        backgroundColor: "color-mix(in srgb, var(--accent) 26%, transparent)",
      },
      ".cm-matchingBracket, &.cm-focused .cm-matchingBracket": {
        backgroundColor: "color-mix(in srgb, var(--accent) 18%, transparent)",
        outline: "none",
        borderRadius: "2px",
      },
      ".cm-tooltip": {
        backgroundColor: "var(--panel)",
        border: "1px solid var(--border)",
        borderRadius: "8px",
        boxShadow: "var(--shadow-float)",
        color: "var(--foreground)",
        overflow: "hidden",
      },
      ".cm-diagnostic": { padding: "6px 10px", fontFamily: "var(--font-sans)", fontSize: "12px" },
    },
    { dark },
  );
  return [theme, syntaxHighlighting(dark ? darkHighlight : lightHighlight)];
}

/** CodeEditorImpl is the CodeMirror 6 editor. Load it only through the lazy CodeEditor. */
export default function CodeEditorImpl({
  value,
  path,
  label,
  readOnly,
  onChange,
  validate,
  onIssues,
  onSave,
  onRun,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const themeSlot = useRef(new Compartment());
  const dark = useHtmlDark();
  const callbacks = useRef({ onChange, validate, onIssues, onSave, onRun });
  const initial = useRef({ value, dark, path, label, readOnly, lint: validate !== undefined });

  useEffect(() => {
    callbacks.current = { onChange, validate, onIssues, onSave, onRun };
  });

  useEffect(() => {
    const init = initial.current;
    const extensions: Extension[] = [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightActiveLine(),
      drawSelection(),
      bracketMatching(),
      languageExtension(languageForPath(init.path)),
      themeSlot.current.of(editorTheme(init.dark)),
      EditorView.contentAttributes.of({ "aria-label": init.label }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) callbacks.current.onChange?.(u.state.doc.toString());
      }),
      // The default keymap binds Mod-Enter to a blank line, so these bindings take precedence.
      Prec.highest(
        keymap.of([
          {
            key: "Mod-s",
            preventDefault: true,
            run: () => {
              callbacks.current.onSave?.();
              return true;
            },
          },
          {
            key: "Mod-Enter",
            run: () => {
              const run = callbacks.current.onRun;
              if (!run) return false;
              run();
              return true;
            },
          },
        ]),
      ),
    ];
    if (init.readOnly) {
      extensions.push(EditorState.readOnly.of(true), EditorView.editable.of(false));
    } else {
      extensions.push(history(), indentOnInput(), keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]));
    }
    if (init.lint) {
      extensions.push(
        lintGutter(),
        linter(
          async (v) => {
            const doc = v.state.doc;
            const fn = callbacks.current.validate;
            if (!fn) return [];
            try {
              const issues = await fn(doc.toString());
              callbacks.current.onIssues?.(issues);
              return issuesToDiagnostics(doc, issues);
            } catch {
              return [];
            }
          },
          { delay: 500 },
        ),
      );
    }
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({ doc: init.value, extensions }),
    });
    view.current = v;
    return () => {
      v.destroy();
      view.current = null;
    };
  }, []);

  useEffect(() => {
    view.current?.dispatch({ effects: themeSlot.current.reconfigure(editorTheme(dark)) });
  }, [dark]);

  useEffect(() => {
    const v = view.current;
    if (!v) return;
    const current = v.state.doc.toString();
    if (current !== value) v.dispatch({ changes: { from: 0, to: current.length, insert: value } });
  }, [value]);

  return <div ref={host} className="h-full min-h-0" />;
}
