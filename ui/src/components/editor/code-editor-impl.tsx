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
import { Compartment, EditorState, type Extension } from "@codemirror/state";
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

const lightHighlight = HighlightStyle.define([
  { tag: [t.keyword, t.controlKeyword, t.modifier], color: "#a626a4" },
  { tag: [t.string, t.special(t.string)], color: "#3f8f3a" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "#986801" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "#6b7280", fontStyle: "italic" },
  { tag: [t.propertyName, t.definition(t.propertyName)], color: "#2f5fc4" },
  { tag: [t.typeName, t.className], color: "#b45309" },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: "#0f6fa8" },
  { tag: [t.operator, t.punctuation], color: "#57534e" },
  { tag: t.invalid, color: "#d64545" },
]);

const darkHighlight = HighlightStyle.define([
  { tag: [t.keyword, t.controlKeyword, t.modifier], color: "#c678dd" },
  { tag: [t.string, t.special(t.string)], color: "#98c379" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "#d19a66" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "#7f848e", fontStyle: "italic" },
  { tag: [t.propertyName, t.definition(t.propertyName)], color: "#61afef" },
  { tag: [t.typeName, t.className], color: "#e5c07b" },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: "#56b6c2" },
  { tag: [t.operator, t.punctuation], color: "#a3aab1" },
  { tag: t.invalid, color: "#ef6b6b" },
]);

function editorTheme(dark: boolean): Extension {
  const theme = EditorView.theme(
    {
      "&": { backgroundColor: "var(--panel)", color: "var(--foreground)", fontSize: "13px", height: "100%" },
      "&.cm-focused": { outline: "none" },
      ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.55" },
      ".cm-content": { caretColor: "var(--foreground)" },
      ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--foreground)" },
      ".cm-gutters": {
        backgroundColor: "var(--muted)",
        color: "var(--muted-foreground)",
        border: "none",
        borderRight: "1px solid var(--border)",
      },
      ".cm-activeLine": { backgroundColor: dark ? "#ffffff08" : "#0000000a" },
      ".cm-activeLineGutter": { backgroundColor: dark ? "#ffffff10" : "#00000010" },
      "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground": {
        backgroundColor: dark ? "#2f6feb55" : "#2f6feb33",
      },
      ".cm-tooltip": { backgroundColor: "var(--panel)", border: "1px solid var(--border)", color: "var(--foreground)" },
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
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const themeSlot = useRef(new Compartment());
  const dark = useHtmlDark();
  const callbacks = useRef({ onChange, validate, onIssues });
  const initial = useRef({ value, dark, path, label, readOnly, lint: validate !== undefined });

  useEffect(() => {
    callbacks.current = { onChange, validate, onIssues };
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
