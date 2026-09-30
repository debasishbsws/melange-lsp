// Runs test/suite.js inside VS Code with this extension loaded and a
// throwaway profile. Uses an installed VS Code when VSCODE_PATH is set or
// found at the macOS default location; otherwise downloads one.
const path = require("path");
const fs = require("fs");
const os = require("os");
const { runTests } = require("@vscode/test-electron");

async function main() {
  const mac = "/Applications/Visual Studio Code.app/Contents/MacOS/Code";
  const exe = process.env.VSCODE_PATH || (fs.existsSync(mac) ? mac : undefined);
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "melange-vscode-"));
  await runTests({
    vscodeExecutablePath: exe,
    extensionDevelopmentPath: path.resolve(__dirname, ".."),
    extensionTestsPath: path.resolve(__dirname, "suite.js"),
    launchArgs: [
      path.resolve(__dirname, "../../../examples/tour"),
      "--disable-extensions",
      "--user-data-dir", userData,
      "--skip-welcome", "--skip-release-notes", "--disable-workspace-trust",
    ],
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
