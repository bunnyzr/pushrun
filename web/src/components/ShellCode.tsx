import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/utils";

type TokenKind = "plain" | "command" | "string" | "variable" | "flag" | "operator" | "comment";

interface Token {
  text: string;
  kind: TokenKind;
}

// tokenizeLine is a deliberately small shell highlighter for read-only
// previews. It covers the constructs used in pushrun steps: comments,
// strings, $VARS, flags and command words; it does not try to be a parser.
function tokenizeLine(line: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  let commandStart = true;

  const push = (text: string, kind: TokenKind) => {
    if (text !== "") tokens.push({ text, kind });
  };

  while (i < line.length) {
    const ch = line[i]!;

    if (/\s/.test(ch)) {
      let end = i + 1;
      while (end < line.length && /\s/.test(line[end]!)) end++;
      push(line.slice(i, end), "plain");
      i = end;
      continue;
    }

    if (ch === "#" && (i === 0 || /\s/.test(line[i - 1]!))) {
      push(line.slice(i), "comment");
      break;
    }

    if (ch === "'" || ch === '"') {
      let end = i + 1;
      while (end < line.length) {
        if (line[end] === "\\" && ch === '"') {
          end += 2;
          continue;
        }
        if (line[end] === ch) {
          end++;
          break;
        }
        end++;
      }
      push(line.slice(i, end), "string");
      i = end;
      commandStart = false;
      continue;
    }

    if (ch === "$") {
      const match = /^\$\{[^}]*\}|^\$[A-Za-z_][A-Za-z0-9_]*/.exec(line.slice(i));
      if (match) {
        push(match[0], "variable");
        i += match[0].length;
        commandStart = false;
        continue;
      }
    }

    const operator = /^(?:&&|\|\||>>|<<|\|&|[;|&><=])/.exec(line.slice(i));
    if (operator) {
      push(operator[0], "operator");
      i += operator[0].length;
      commandStart = operator[0] === "&&" || operator[0] === "||" || operator[0] === "|" || operator[0] === ";";
      continue;
    }

    let end = i;
    while (end < line.length) {
      const next = line[end]!;
      if (/\s/.test(next) || "$#'\"|&;<>=".includes(next)) break;
      end++;
    }
    const word = line.slice(i, end);
    if (word === "") {
      push(ch, "plain");
      i++;
      continue;
    }
    if (commandStart) {
      push(word, "command");
      commandStart = false;
    } else if (word.startsWith("-")) {
      push(word, "flag");
    } else {
      push(word, "plain");
    }
    i = end;
  }

  return tokens;
}

const TOKEN_CLASS: Record<TokenKind, string> = {
  plain: "",
  command: "text-[#1f4f82] font-medium",
  string: "text-[#0a6b5c]",
  variable: "text-[#0550ae]",
  flag: "text-[#6f42c1]",
  operator: "text-muted-foreground",
  comment: "text-[#6e7781] italic",
};

interface ShellCodeProps {
  code: string;
  className?: string;
}

// ShellCode renders a read-only shell snippet with the same palette as the
// editor, so the overview and the pipeline view read as one surface.
export default function ShellCode({ code, className }: ShellCodeProps) {
  const lines = code.split("\n");
  return (
    <pre
      className={cn(
        "overflow-auto rounded-md border bg-secondary px-3 py-2.5 font-mono text-[13px] leading-[1.65] text-foreground",
        className,
      )}
    >
      {lines.map((line, lineIndex) => (
        <Fragment key={lineIndex}>
          {lineIndex > 0 && "\n"}
          {tokenizeLine(line).map((token, tokenIndex): ReactNode => (
            <span key={tokenIndex} className={TOKEN_CLASS[token.kind]}>
              {token.text}
            </span>
          ))}
        </Fragment>
      ))}
    </pre>
  );
}
