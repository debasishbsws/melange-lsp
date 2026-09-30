// Integration checks run inside VS Code against examples/tour.
const vscode = require("vscode");
const path = require("path");
const assert = require("assert");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function until(what, fn, timeout = 20000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    const v = await fn();
    if (v) return v;
    await sleep(200);
  }
  throw new Error("timed out waiting for " + what);
}

// isHidden reports whether line is outside the active editor's visible ranges.
function isHidden(editor, line) {
  return !editor.visibleRanges.some((r) => r.start.line <= line && line <= r.end.line);
}

// noteFolded: the note's heading line (0) shows and its body (1) does not,
// so scrolling cannot fake it.
function noteFolded() {
  const e = vscode.window.activeTextEditor;
  return e && !isHidden(e, 0) && isHidden(e, 1);
}
function noteOpen() {
  const e = vscode.window.activeTextEditor;
  return e && !isHidden(e, 0) && !isHidden(e, 1);
}

exports.run = async function () {
  const file = path.resolve(__dirname, "../../../examples/tour/hello.yaml");
  const uri = vscode.Uri.file(file);
  const results = [];
  const check = (name, fn) => results.push([name, fn]);

  let editor = await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(uri));

  check("server publishes diagnostics", async () => {
    const diags = await until("diagnostics", () => {
      const d = vscode.languages.getDiagnostics(uri).filter((x) => x.source === "melange");
      return d.length > 0 && d;
    });
    assert(diags.some((d) => d.message.includes('did you mean "usrlocal"')), "usrloca diagnostic");
  });

  check("hover shows pipeline inputs", async () => {
    const line = editor.document.getText().split("\n").findIndex((l) => l.includes("uses: local/greet"));
    const hovers = await vscode.commands.executeCommand("vscode.executeHoverProvider", uri, new vscode.Position(line, 12));
    const text = hovers.flatMap((h) => h.contents.map((c) => c.value ?? String(c))).join("\n");
    assert(text.includes("Who to greet"), "hover text: " + text.slice(0, 200));
  });

  check("agent notes are folded on open", async () => {
    await until("note folded", noteFolded);
  });

  check("notes fold again after unfold, close and reopen", async () => {
    await vscode.commands.executeCommand("melange.unfoldAgentNotes");
    await until("note unfolded", noteOpen);
    // Open another file first so closing does not leave the window empty.
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument({ content: "scratch" }), { preview: false });
    await vscode.window.showTextDocument(editor.document);
    await vscode.commands.executeCommand("workbench.action.closeActiveEditor");
    await sleep(300);
    editor = await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(uri));
    await until("note folded after reopen", noteFolded);
  });

  check("toggle command unfolds and folds", async () => {
    await vscode.commands.executeCommand("melange.toggleAgentNotes");
    await until("note unfolded by toggle", noteOpen);
    await vscode.commands.executeCommand("melange.toggleAgentNotes");
    await until("note folded by toggle", noteFolded);
  });

  // PAUSE=seconds leaves the notes folded on screen, for a screenshot.
  if (process.env.PAUSE) {
    check("pause", async () => {
      await vscode.commands.executeCommand("melange.foldAgentNotes");
      await sleep(Number(process.env.PAUSE) * 1000);
    });
  }

  let failed = 0;
  for (const [name, fn] of results) {
    try {
      await fn();
      console.log("  ok   " + name);
    } catch (err) {
      failed++;
      console.log("  FAIL " + name + ": " + err.message);
    }
  }
  if (failed) throw new Error(failed + " check(s) failed");
};
