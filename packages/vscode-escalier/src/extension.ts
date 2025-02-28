/* --------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 * ------------------------------------------------------------------------------------------ */

import * as path from "path";
import { workspace, ExtensionContext } from "vscode";

import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient;

export function activate(context: ExtensionContext) {
  // The server is implemented in node
  //   const serverModule = context.asAbsolutePath(
  //     path.join("server", "out", "server.js")
  //   );
  const serverModule = context.asAbsolutePath(
    path.join("..", "..", "bin", "lsp-server")
  );

  // If the extension is launched in debug mode then the debug server options are used
  // Otherwise the run options are used
  // const serverOptions: ServerOptions = {
  // 	run: { module: serverModule, transport: TransportKind.ipc },
  // 	debug: {
  // 		module: serverModule,
  // 		transport: TransportKind.ipc,
  // 	},
  // };

  const executable = {
    command: serverModule,
    // options: { cwd: workspace. },
    args: ["--stdio"],
    transport: TransportKind.stdio,
  };

  const serverOptions = {
    run: executable,
    debug: executable,
  };

  // Options to control the language client
  const clientOptions: LanguageClientOptions = {
    // Register the server for plain text documents
    documentSelector: [{ scheme: "file", language: "escalier" }],
    synchronize: {
      // Notify the server about file changes to '.clientrc files contained in the workspace
      fileEvents: workspace.createFileSystemWatcher("**/.clientrc"),
    },
  };

  // Create the language client and start the client.
  client = new LanguageClient(
    "escalier",
    "Escalier",
    serverOptions,
    clientOptions
  );

  client.outputChannel.appendLine("Client started");

  // Start the client. This will also launch the server
  client.start();
}

export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  return client.stop();
}
