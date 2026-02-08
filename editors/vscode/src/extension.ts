import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(context: vscode.ExtensionContext) {
  const config = vscode.workspace.getConfiguration("dang.lsp");

  if (!config.get<boolean>("enabled", true)) {
    return;
  }

  const command = config.get<string>("path", "dang");
  const args = ["--lsp"];

  const logFile = config.get<string>("logFile", "");
  if (logFile) {
    args.push("--lsp-log-file", logFile);
  }

  const serverOptions: ServerOptions = {
    command,
    args,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "dang" }],
  };

  client = new LanguageClient("dang", "Dang", serverOptions, clientOptions);
  client.start();

  context.subscriptions.push({
    dispose: () => {
      if (client) {
        client.stop();
      }
    },
  });
}

export function deactivate(): Thenable<void> | undefined {
  if (client) {
    return client.stop();
  }
  return undefined;
}
