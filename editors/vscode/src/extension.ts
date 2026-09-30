// VS Code extension for melange-lsp: starts the language server for YAML
// files and keeps @agent notes folded.
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

// Documents whose notes were folded since they were opened, so notes fold
// once per open and stay as the user leaves them afterwards.
const foldedSinceOpen = new Set<string>();
// Per-document state for the toggle command.
const notesFolded = new Map<string, boolean>();

function config() {
  return vscode.workspace.getConfiguration("melange");
}

// Options the server reads, from the melange.* settings.
function serverSettings() {
  const c = config();
  return {
    pipelineDirs: c.get<string[]>("pipelineDirs", []),
    inlayHints: c.get<boolean>("inlayHints", true),
  };
}

function createClient(): LanguageClient {
  const command = config().get<string>("server.path") || "melange-lsp";
  const serverOptions: ServerOptions = { command, args: [] };
  const clientOptions: LanguageClientOptions = {
    // The server ignores YAML that is not a melange package or pipeline.
    documentSelector: [{ scheme: "file", language: "yaml" }],
    initializationOptions: serverSettings(),
    outputChannelName: "melange-lsp",
    middleware: {
      workspace: {
        // Answer the server's configuration requests with our options.
        configuration: () => [serverSettings()],
      },
    },
  };
  return new LanguageClient("melange", "melange-lsp", serverOptions, clientOptions);
}

async function startClient(): Promise<void> {
  client = createClient();
  try {
    await client.start();
  } catch (err) {
    const path = config().get<string>("server.path") || "melange-lsp";
    void vscode.window.showErrorMessage(
      `Could not start melange-lsp (${path}). Install it with "go install ./cmd/melange-lsp" from the melange-lsp repository, or set melange.server.path.`,
    );
    client = undefined;
  }
}

// An agent note fold as the server reports it: comment folds labelled
// with the robot, e.g. "🤖 NOTE: Keep the patch".
type Note = { line: number; label: string };

// Notes per document version, for decorating folded notes.
const noteCache = new Map<string, { version: number; notes: Note[] }>();

async function agentNotes(doc: vscode.TextDocument): Promise<Note[]> {
  if (!client || doc.languageId !== "yaml") {
    return [];
  }
  type Range = { startLine: number; kind?: string; collapsedText?: string };
  const ranges = (await client.sendRequest("textDocument/foldingRange", {
    textDocument: { uri: doc.uri.toString() },
  })) as Range[] | null;
  const notes = (ranges ?? [])
    .filter((r) => r.kind === "comment" && r.collapsedText?.startsWith("🤖"))
    .map((r) => ({ line: r.startLine, label: r.collapsedText as string }));
  noteCache.set(doc.uri.toString(), { version: doc.version, notes });
  return notes;
}

async function agentNoteLines(doc: vscode.TextDocument): Promise<number[]> {
  return (await agentNotes(doc)).map((n) => n.line);
}

// VS Code shows a folded region's first line as it is. For folded notes we
// hide that line's text and draw the server's label in its place, unless
// the cursor is on the line.
const hiddenText = vscode.window.createTextEditorDecorationType({
  textDecoration: "none; display: none;",
});
const noteLabel = vscode.window.createTextEditorDecorationType({
  before: {
    color: new vscode.ThemeColor("editorCodeLens.foreground"),
    fontStyle: "italic",
  },
});

// The note line the user moved the cursor onto, per document; it shows
// its real text so it can be edited. Opening a file puts the cursor on
// line 0 without the user choosing it, so only keyboard and mouse moves
// count.
const revealed = new Map<string, number>();

function decorateNotes(editor: vscode.TextEditor): void {
  const cached = noteCache.get(editor.document.uri.toString());
  if (!cached || cached.version !== editor.document.version) {
    editor.setDecorations(hiddenText, []);
    editor.setDecorations(noteLabel, []);
    if (editor.document.languageId === "yaml") {
      void agentNotes(editor.document).then(() => {
        const again = noteCache.get(editor.document.uri.toString());
        if (again?.version === editor.document.version) {
          decorateNotes(editor);
        }
      });
    }
    return;
  }
  const shown = (line: number) => editor.visibleRanges.some((r) => r.start.line <= line && line <= r.end.line);
  const reveal = revealed.get(editor.document.uri.toString());
  const hide: vscode.Range[] = [];
  const labels: vscode.DecorationOptions[] = [];
  for (const n of cached.notes) {
    if (!shown(n.line) || shown(n.line + 1) || reveal === n.line) {
      continue; // unfolded, out of view, or being edited
    }
    const text = editor.document.lineAt(n.line).text;
    const indent = text.length - text.trimStart().length;
    hide.push(new vscode.Range(n.line, indent, n.line, text.length));
    labels.push({
      range: new vscode.Range(n.line, indent, n.line, indent),
      renderOptions: { before: { contentText: n.label } },
    });
  }
  editor.setDecorations(hiddenText, hide);
  editor.setDecorations(noteLabel, labels);
}

async function setNotesFolded(editor: vscode.TextEditor, fold: boolean): Promise<boolean> {
  const lines = await agentNoteLines(editor.document);
  if (lines.length === 0) {
    return false;
  }
  // editor.fold works on the active editor and needs VS Code's folding
  // model, which it computes from our folding ranges on demand.
  if (vscode.window.activeTextEditor !== editor) {
    await vscode.window.showTextDocument(editor.document, editor.viewColumn);
  }
  await vscode.commands.executeCommand(fold ? "editor.fold" : "editor.unfold", {
    selectionLines: lines,
    levels: 1,
  });
  notesFolded.set(editor.document.uri.toString(), fold);
  decorateNotes(editor);
  return true;
}

function tabUri(tab: vscode.Tab): string | undefined {
  return tab.input instanceof vscode.TabInputText ? tab.input.uri.toString() : undefined;
}

function isOpenInTab(uri: string): boolean {
  return vscode.window.tabGroups.all.some((g) => g.tabs.some((t) => tabUri(t) === uri));
}

// notesAreFolded reads the fold state from the editor: a note in view is
// folded when its first line shows but the next is hidden. Notes out of
// view fall back to the last state this extension set.
async function notesAreFolded(editor: vscode.TextEditor, fallback = true): Promise<boolean> {
  const shown = (line: number) => editor.visibleRanges.some((r) => r.start.line <= line && line <= r.end.line);
  for (const line of await agentNoteLines(editor.document)) {
    if (shown(line)) {
      return !shown(line + 1);
    }
  }
  // No note in view: trust the last state set, or assume folded when
  // checking a fold that was just requested.
  return fallback ? (notesFolded.get(editor.document.uri.toString()) ?? false) : true;
}

async function foldOnOpen(editor: vscode.TextEditor | undefined): Promise<void> {
  if (!editor || editor.document.languageId !== "yaml" || !config().get<boolean>("agentNotes.foldOnOpen", true)) {
    return;
  }
  const key = editor.document.uri.toString();
  if (foldedSinceOpen.has(key)) {
    return;
  }
  foldedSinceOpen.add(key);
  // Right after a file opens, the server may not have answered yet and
  // VS Code's folding model may not be built, in which case editor.fold
  // silently does nothing. Retry until the notes are seen folded.
  for (const delay of [0, 150, 300, 600, 1000, 1500, 2500]) {
    await new Promise((r) => setTimeout(r, delay));
    if (vscode.window.activeTextEditor?.document !== editor.document) {
      foldedSinceOpen.delete(key); // fold when the user comes back to it
      return;
    }
    const active = vscode.window.activeTextEditor;
    if (!(await setNotesFolded(active, true))) {
      continue; // no notes known yet
    }
    await new Promise((r) => setTimeout(r, 50));
    if (await notesAreFolded(active, false)) {
      return;
    }
  }
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  await startClient();

  const withEditor = (fn: (e: vscode.TextEditor) => Promise<unknown>) => async () => {
    const editor = vscode.window.activeTextEditor;
    if (editor) {
      await fn(editor);
    }
  };

  context.subscriptions.push(
    hiddenText,
    noteLabel,
    // Folding changes the visible ranges, so this also tracks fold state.
    vscode.window.onDidChangeTextEditorVisibleRanges((e) => decorateNotes(e.textEditor)),
    vscode.window.onDidChangeTextEditorSelection((e) => {
      const key = e.textEditor.document.uri.toString();
      if (e.kind === vscode.TextEditorSelectionChangeKind.Keyboard || e.kind === vscode.TextEditorSelectionChangeKind.Mouse) {
        revealed.set(key, e.selections[0].active.line);
      }
      decorateNotes(e.textEditor);
    }),
    vscode.workspace.onDidChangeTextDocument((e) => {
      for (const editor of vscode.window.visibleTextEditors) {
        if (editor.document === e.document) {
          decorateNotes(editor);
        }
      }
    }),
    vscode.commands.registerCommand("melange.foldAgentNotes", withEditor((e) => setNotesFolded(e, true))),
    vscode.commands.registerCommand("melange.unfoldAgentNotes", withEditor((e) => setNotesFolded(e, false))),
    vscode.commands.registerCommand(
      "melange.toggleAgentNotes",
      withEditor(async (e) => setNotesFolded(e, !(await notesAreFolded(e)))),
    ),
    vscode.commands.registerCommand("melange.restartServer", async () => {
      await client?.stop();
      await startClient();
    }),

    vscode.window.onDidChangeActiveTextEditor((e) => void foldOnOpen(e)),
    // Closing a file's last tab forgets its fold state, so reopening folds
    // notes again. Tab events fire at once; document close events can lag
    // far behind because VS Code keeps closed documents alive.
    vscode.window.tabGroups.onDidChangeTabs((e) => {
      for (const tab of e.closed) {
        const uri = tabUri(tab);
        if (uri && !isOpenInTab(uri)) {
          foldedSinceOpen.delete(uri);
          notesFolded.delete(uri);
        }
      }
    }),
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration("melange.server.path")) {
        await client?.stop();
        await startClient();
      } else if (e.affectsConfiguration("melange") && client) {
        await client.sendNotification("workspace/didChangeConfiguration", { settings: { melange: serverSettings() } });
      }
    }),
  );

  void foldOnOpen(vscode.window.activeTextEditor);
}

export async function deactivate(): Promise<void> {
  await client?.stop();
}
