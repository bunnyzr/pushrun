import { useEffect, useRef } from "react";
import { EditorState } from "@codemirror/state";
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  placeholder as placeholderExt,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import {
  HighlightStyle,
  StreamLanguage,
  syntaxHighlighting,
} from "@codemirror/language";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { tags } from "@lezer/highlight";
import { cn } from "@/lib/utils";

// High-contrast shell theme: commands and variables stay readable without
// turning keywords into error-red text.
const shellHighlight = HighlightStyle.define([
  { tag: tags.comment, color: "#6e7781", fontStyle: "italic" },
  { tag: tags.string, color: "#0a6b5c" },
  { tag: [tags.keyword, tags.operatorKeyword], color: "#6f42c1" },
  { tag: tags.variableName, color: "#0550ae" },
  { tag: [tags.function(tags.variableName), tags.number, tags.operator], color: "#0550ae" },
  { tag: tags.bool, color: "#0550ae" },
]);

export interface ScriptEditorProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  "aria-label"?: string;
  className?: string;
}

// Light-surface script editor for bash snippets: line numbers, shell
// highlight, grows with content between ~120px and ~400px.
export default function ScriptEditor({
  value,
  onChange,
  placeholder,
  "aria-label": ariaLabel,
  className,
}: ScriptEditorProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  // updateListener closes over this ref so the editor is never rebuilt just
  // because the parent re-rendered with a new onChange identity.
  const onChangeRef = useRef(onChange);
  const placeholderRef = useRef(placeholder);
  useEffect(() => {
    onChangeRef.current = onChange;
  });
  useEffect(() => {
    placeholderRef.current = placeholder;
  });

  useEffect(() => {
    if (!hostRef.current) return;
    const view = new EditorView({
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
      EditorView.lineWrapping,
          highlightActiveLine(),
          highlightActiveLineGutter(),
          history(),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          StreamLanguage.define(shell),
          syntaxHighlighting(shellHighlight),
          placeholderRef.current ? placeholderExt(placeholderRef.current) : [],
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              onChangeRef.current(update.state.doc.toString());
            }
          }),
          EditorView.theme({
            "&": {
              backgroundColor: "transparent",
              fontSize: "13px",
            },
            ".cm-content": {
              fontFamily: "var(--font-mono)",
              padding: "8px 0",
              caretColor: "var(--color-primary)",
            },
            ".cm-line": {
              paddingLeft: "8px",
            },
            ".cm-scroller": {
              overflow: "auto",
              maxHeight: "400px",
              fontFamily: "inherit",
            },
            ".cm-gutters": {
              backgroundColor: "var(--secondary)",
              border: "none",
              borderRight: "1px solid var(--border)",
              color: "var(--muted-foreground)",
            },
            ".cm-activeLine": {
              backgroundColor: "var(--accent)",
            },
            ".cm-activeLineGutter": {
              backgroundColor: "var(--accent)",
            },
            "&.cm-focused": {
              outline: "none",
            },
          }),
        ],
      }),
      parent: hostRef.current,
    });
    if (ariaLabel) {
      view.contentDOM.setAttribute("aria-label", ariaLabel);
    }
    // The scroll container must be reachable by keyboard (axe
    // scrollable-region-focusable); the editable content stays tabbable.
    view.scrollDOM.tabIndex = 0;
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // The editor mounts once per host; value/label changes sync below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Adopt external value changes (draft reset, project switch) without
  // clobbering in-flight typing: only dispatch when the docs differ.
  useEffect(() => {
    const view = viewRef.current;
    if (view && view.state.doc.toString() !== value) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
    }
  }, [value]);

  useEffect(() => {
    if (ariaLabel) {
      viewRef.current?.contentDOM.setAttribute("aria-label", ariaLabel);
    }
  }, [ariaLabel]);

  return (
    <div
      ref={hostRef}
      className={cn(
        "overflow-hidden rounded-md border border-input bg-card transition-shadow focus-within:ring-2 focus-within:ring-ring",
        className,
      )}
    />
  );
}
