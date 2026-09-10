// IC10 Go (.icg) VSCode extension.
//
// Provides syntax highlighting (via the TextMate grammar) and a small
// Language Server Protocol client that talks to `ic10c lsp`. It is written in
// plain JavaScript with no npm dependencies, so it works as soon as the
// `ic10c` binary is available.
const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

/** @type {LspClient|undefined} */
let client;

function activate(context) {
    client = new LspClient();
    context.subscriptions.push(client);
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.restartServer', () => client.restart())
    );
    client.start();
}

function deactivate() {
    if (client) {
        client.dispose();
        client = undefined;
    }
}

class LspClient {
    constructor() {
        this.proc = undefined;
        this.buffer = Buffer.alloc(0);
        this.nextId = 1;
        this.pending = new Map();
        this.initialized = false;
        this.output = vscode.window.createOutputChannel('IC10 Go');
        this.diags = vscode.languages.createDiagnosticCollection('icg');

        this.disposables = [this.output, this.diags];
        this.disposables.push(
            vscode.workspace.onDidOpenTextDocument((d) => this.onOpen(d))
        );
        this.disposables.push(
            vscode.workspace.onDidChangeTextDocument((e) => this.onChange(e))
        );
        this.disposables.push(
            vscode.workspace.onDidCloseTextDocument((d) => this.onClose(d))
        );
        this.disposables.push(
            vscode.languages.registerCompletionItemProvider('icg', {
                provideCompletionItems: (doc, pos) => this.completion(doc, pos),
            })
        );
    }

    start() {
        const serverPath = this.resolveServer();
        this.output.appendLine(`starting language server: ${serverPath}`);

        try {
            this.proc = cp.spawn(serverPath, ['lsp'], { stdio: ['pipe', 'pipe', 'pipe'] });
        } catch (err) {
            this.reportMissingServer(err);
            return;
        }

        this.proc.on('error', (err) => this.reportMissingServer(err));
        this.proc.stdout.on('data', (d) => this.onData(d));
        this.proc.stderr.on('data', (d) => this.output.append(d.toString()));
        this.proc.on('exit', (code) => {
            this.output.appendLine(`language server exited (code ${code})`);
            this.proc = undefined;
            this.initialized = false;
        });

        this.request('initialize', {
            processId: process.pid,
            rootUri: null,
            capabilities: {
                textDocument: { completion: { completionItem: { snippetSupport: false } } },
            },
        })
            .then(() => {
                this.initialized = true;
                this.notify('initialized', {});
                for (const doc of vscode.workspace.textDocuments) {
                    if (doc.languageId === 'icg') this.sendDidOpen(doc);
                }
            })
            .catch((err) => this.output.appendLine(`initialize failed: ${err.message}`));
    }

    restart() {
        if (this.proc) {
            this.proc.kill();
            this.proc = undefined;
        }
        this.initialized = false;
        this.buffer = Buffer.alloc(0);
        this.diags.clear();
        this.start();
    }

    resolveServer() {
        const configured = vscode.workspace.getConfiguration('icg').get('serverPath');
        if (configured) {
            if (fs.existsSync(configured)) return configured;
            this.output.appendLine(`configured icg.serverPath does not exist: ${configured}`);
        }

        // Walk up from every workspace folder and from the active file.
        const starts = [];
        for (const folder of vscode.workspace.workspaceFolders || []) {
            starts.push(folder.uri.fsPath);
        }
        const active = vscode.window.activeTextEditor;
        if (active && active.document.uri.scheme === 'file') {
            starts.push(path.dirname(active.document.uri.fsPath));
        }
        for (const start of starts) {
            const found = findUp(start);
            if (found) return found;
        }

        // Common install locations.
        for (const p of commonServerPaths()) {
            if (fs.existsSync(p)) return p;
        }

        return 'ic10c'; // rely on PATH
    }

    reportMissingServer(err) {
        this.output.appendLine(`cannot start ic10c: ${err.message}`);
        this.output.appendLine('searched: icg.serverPath, workspace folders, parent directories, ~/go/bin, $GOPATH/bin, PATH');
        vscode.window
            .showWarningMessage(
                'IC10 Go: cannot find the ic10c executable. Build it with "go build -o ic10c ./cmd/ic10c" or set "icg.serverPath".',
                'Open Settings'
            )
            .then((choice) => {
                if (choice === 'Open Settings') {
                    vscode.commands.executeCommand('workbench.action.openSettings', 'icg.serverPath');
                }
            });
    }

    // -- document events ----------------------------------------------------

    onOpen(doc) {
        if (doc.languageId === 'icg' && this.initialized) this.sendDidOpen(doc);
    }

    onChange(e) {
        if (e.document.languageId !== 'icg' || !this.initialized) return;
        this.notify('textDocument/didChange', {
            textDocument: { uri: e.document.uri.toString() },
            contentChanges: [{ text: e.document.getText() }],
        });
    }

    onClose(doc) {
        if (doc.languageId !== 'icg') return;
        this.diags.delete(doc.uri);
        if (this.initialized) {
            this.notify('textDocument/didClose', {
                textDocument: { uri: doc.uri.toString() },
            });
        }
    }

    sendDidOpen(doc) {
        this.notify('textDocument/didOpen', {
            textDocument: {
                uri: doc.uri.toString(),
                languageId: 'icg',
                version: doc.version,
                text: doc.getText(),
            },
        });
    }

    // -- completion ---------------------------------------------------------

    async completion(doc, pos) {
        if (!this.initialized) return [];
        try {
            const res = await this.request('textDocument/completion', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            const items = Array.isArray(res) ? res : (res && res.items) || [];
            return items.map((i) => {
                const item = new vscode.CompletionItem(i.label, i.kind || 1);
                if (i.detail) item.detail = i.detail;
                return item;
            });
        } catch (err) {
            return [];
        }
    }

    // -- JSON-RPC -----------------------------------------------------------

    request(method, params) {
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            this.pending.set(id, { resolve, reject });
            this.send({ jsonrpc: '2.0', id, method, params });
        });
    }

    notify(method, params) {
        this.send({ jsonrpc: '2.0', method, params });
    }

    send(obj) {
        if (!this.proc || !this.proc.stdin.writable) return;
        const data = Buffer.from(JSON.stringify(obj), 'utf8');
        this.proc.stdin.write(`Content-Length: ${data.length}\r\n\r\n`);
        this.proc.stdin.write(data);
    }

    onData(chunk) {
        this.buffer = Buffer.concat([this.buffer, chunk]);
        for (;;) {
            const headerEnd = this.buffer.indexOf('\r\n\r\n');
            if (headerEnd < 0) return;
            const header = this.buffer.slice(0, headerEnd).toString('ascii');
            const match = /Content-Length:\s*(\d+)/i.exec(header);
            if (!match) {
                this.buffer = this.buffer.slice(headerEnd + 4);
                continue;
            }
            const length = parseInt(match[1], 10);
            const start = headerEnd + 4;
            if (this.buffer.length < start + length) return;
            const body = this.buffer.slice(start, start + length).toString('utf8');
            this.buffer = this.buffer.slice(start + length);
            let msg;
            try {
                msg = JSON.parse(body);
            } catch (err) {
                continue;
            }
            this.handle(msg);
        }
    }

    handle(msg) {
        if (msg.id !== undefined && (msg.result !== undefined || msg.error !== undefined)) {
            const pending = this.pending.get(msg.id);
            if (pending) {
                this.pending.delete(msg.id);
                if (msg.error) pending.reject(new Error(msg.error.message));
                else pending.resolve(msg.result);
            }
            return;
        }
        if (msg.method === 'textDocument/publishDiagnostics') {
            this.publishDiagnostics(msg.params);
        }
    }

    publishDiagnostics(params) {
        const uri = vscode.Uri.parse(params.uri);
        const diagnostics = (params.diagnostics || []).map((d) => {
            const range = new vscode.Range(
                d.range.start.line,
                d.range.start.character,
                d.range.end.line,
                d.range.end.character
            );
            const severity =
                d.severity === 1
                    ? vscode.DiagnosticSeverity.Error
                    : d.severity === 2
                      ? vscode.DiagnosticSeverity.Warning
                      : vscode.DiagnosticSeverity.Information;
            const diag = new vscode.Diagnostic(range, d.message, severity);
            diag.source = d.source || 'ic10c';
            return diag;
        });
        this.diags.set(uri, diagnostics);
    }

    dispose() {
        if (this.proc) this.proc.kill();
        for (const d of this.disposables) d.dispose();
    }
}

module.exports = { activate, deactivate };

// findUp looks for an ic10c binary in start and its parent directories.
function findUp(start) {
    let dir = start;
    for (let i = 0; i < 8; i++) {
        for (const name of ['ic10c', 'ic10c.exe']) {
            const p = path.join(dir, name);
            if (fs.existsSync(p)) return p;
        }
        const parent = path.dirname(dir);
        if (parent === dir) break;
        dir = parent;
    }
    return undefined;
}

// commonServerPaths lists likely install locations for the ic10c binary.
function commonServerPaths() {
    const home = os.homedir();
    const paths = [];
    if (process.env.GOPATH) paths.push(path.join(process.env.GOPATH, 'bin', 'ic10c'));
    paths.push(path.join(home, 'go', 'bin', 'ic10c'));
    paths.push(path.join(home, 'bin', 'ic10c'));
    paths.push('/usr/local/bin/ic10c');
    return paths;
}
