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
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.compile', () => client.compile())
    );
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.run', () => client.run())
    );
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.decompile', () => client.decompile())
    );
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.minify', () => client.minify())
    );
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.disasm', () => client.disasm())
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
        this.pendingChanges = new Map();
        this.changeTimer = undefined;

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
        this.disposables.push(
            vscode.languages.registerDocumentFormattingEditProvider('icg', {
                provideDocumentFormattingEdits: (doc) => this.formatting(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerHoverProvider('icg', {
                provideHover: (doc, pos) => this.hover(doc, pos),
            })
        );
        this.disposables.push(
            vscode.languages.registerDefinitionProvider('icg', {
                provideDefinition: (doc, pos) => this.definition(doc, pos),
            })
        );
        this.disposables.push(
            vscode.languages.registerDocumentSymbolProvider('icg', {
                provideDocumentSymbols: (doc) => this.documentSymbols(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerFoldingRangeProvider('icg', {
                provideFoldingRanges: (doc) => this.foldingRanges(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerReferenceProvider('icg', {
                provideReferences: (doc, pos) => this.references(doc, pos),
            })
        );
        this.disposables.push(
            vscode.languages.registerRenameProvider('icg', {
                provideRenameEdits: (doc, pos, newName) => this.rename(doc, pos, newName),
            })
        );
        this.disposables.push(
            vscode.languages.registerSignatureHelpProvider(
                'icg',
                { provideSignatureHelp: (doc, pos) => this.signatureHelp(doc, pos) },
                '(',
                ','
            )
        );
        this.disposables.push(
            vscode.languages.registerCodeActionsProvider('icg', {
                provideCodeActions: (doc, range, context) => this.codeActions(doc, range, context),
            })
        );
        this.disposables.push(
            vscode.languages.registerInlayHintsProvider('icg', {
                provideInlayHints: (doc) => this.inlayHints(doc),
            })
        );
    }

    start() {
        const serverPath = this.resolveServer();
        this.output.appendLine(`starting language server: ${serverPath}`);

        try {
            const env = Object.assign({}, process.env);
            if (this.config().noCheck) env.IC10C_NO_CHECK = '1';
            this.proc = cp.spawn(serverPath, ['lsp'], { stdio: ['pipe', 'pipe', 'pipe'], env });
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
            locale: vscode.env.language,
            capabilities: {
                textDocument: { completion: { completionItem: { snippetSupport: false } } },
            },
        })
            .then((result) => {
                this.initialized = true;
                this.notify('initialized', {});
                this.registerSemanticTokens(result);
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

    config() {
        const c = vscode.workspace.getConfiguration('icg');
        return {
            stableIns: c.get('stableIns'),
            noCheck: c.get('noCheck'),
        };
    }

    // execCli runs the ic10c binary with the given arguments.
    execCli(args) {
        const serverPath = this.resolveServer();
        const env = Object.assign({}, process.env);
        if (this.config().noCheck) env.IC10C_NO_CHECK = '1';
        return new Promise((resolve) => {
            cp.execFile(serverPath, args, { env, maxBuffer: 8 * 1024 * 1024 }, (err, stdout, stderr) => {
                resolve({ code: err ? err.code || 1 : 0, stdout: stdout || '', stderr: stderr || '' });
            });
        });
    }

    // activeICG returns the active .icg document or undefined.
    activeICG() {
        const editor = vscode.window.activeTextEditor;
        if (!editor || editor.document.languageId !== 'icg') {
            vscode.window.showWarningMessage(t('IC10 Go: open a .icg file first.', 'IC10 Go: 请先打开一个 .icg 文件。'));
            return undefined;
        }
        return editor.document;
    }

    // withTempFile writes the document to a temp .icg file and calls fn(path).
    async withTempFile(doc, fn) {
        const tmp = path.join(os.tmpdir(), `icg-${process.pid}-${Date.now()}.icg`);
        fs.writeFileSync(tmp, doc.getText(), 'utf8');
        try {
            return await fn(tmp);
        } finally {
            try {
                fs.unlinkSync(tmp);
            } catch (err) {
                // ignore
            }
        }
    }

    // compile runs `ic10c build` + `ic10c stats` and previews the result.
    async compile() {
        const doc = this.activeICG();
        if (!doc) return;
        await this.withTempFile(doc, async (tmp) => {
            const buildArgs = ['build'];
            if (this.config().stableIns) buildArgs.push('--stable-ins');
            buildArgs.push(tmp);
            const build = await this.execCli(buildArgs);
            if (build.code !== 0) {
                this.output.appendLine(`=== compile failed: ${path.basename(doc.fileName)} ===\n${build.stderr}`);
                this.output.show(true);
                vscode.window.showErrorMessage(t('IC10 Go: compilation failed. See the "IC10 Go" output.', 'IC10 Go: 编译失败，详见 "IC10 Go" 输出面板。'));
                return;
            }
            const stats = await this.execCli(['stats', tmp]);
            const preview = await vscode.workspace.openTextDocument({
                content: build.stdout,
                language: 'plaintext',
            });
            await vscode.window.showTextDocument(preview, {
                viewColumn: vscode.ViewColumn.Beside,
                preview: true,
            });
            this.output.appendLine(`=== ${path.basename(doc.fileName)} ===\n${stats.stdout.trim()}`);
            const lines = stats.stdout.split('\n').find((l) => l.trim().startsWith('lines'));
            vscode.window.setStatusBarMessage(`IC10 Go: ${lines ? lines.trim() : t('compiled', '已编译')}`, 5000);
        });
    }

    // run compiles and executes the document in the built-in VM.
    async run() {
        const doc = this.activeICG();
        if (!doc) return;
        await this.withTempFile(doc, async (tmp) => {
            const args = ['run'];
            if (this.config().stableIns) args.push('--stable-ins');
            args.push(tmp);
            const res = await this.execCli(args);
            this.output.appendLine(`=== run: ${path.basename(doc.fileName)} ===\n${res.stdout}${res.stderr}`);
            this.output.show(true);
        });
    }

    // activeDoc returns the active document whose language is in langs.
    activeDoc(langs) {
        const editor = vscode.window.activeTextEditor;
        if (!editor || !langs.includes(editor.document.languageId)) {
            vscode.window.showWarningMessage(t('IC10 Go: open a ' + langs.map((l) => '.' + l).join(' / ') + ' file first.', 'IC10 Go: 请先打开一个 ' + langs.map((l) => '.' + l).join(' / ') + ' 文件。'));
            return undefined;
        }
        return editor.document;
    }

    // runTool runs a CLI subcommand on a temp copy of the document and opens
    // the result in a new editor.
    async runTool(doc, args, outputLanguage) {
        await this.withTempFile(doc, async (tmp) => {
            const res = await this.execCli(args.concat(tmp));
            if (res.code !== 0) {
                this.output.appendLine(`=== ${args[0]} failed ===\n${res.stderr}`);
                this.output.show(true);
                vscode.window.showErrorMessage(t('IC10 Go: ' + args[0] + ' failed. See the "IC10 Go" output.', 'IC10 Go: ' + args[0] + ' 执行失败，详见 "IC10 Go" 输出面板。'));
                return;
            }
            const preview = await vscode.workspace.openTextDocument({
                content: res.stdout,
                language: outputLanguage,
            });
            await vscode.window.showTextDocument(preview, {
                viewColumn: vscode.ViewColumn.Beside,
                preview: false,
            });
        });
    }

    async decompile() {
        const doc = this.activeDoc(['ic10']);
        if (doc) await this.runTool(doc, ['decompile', '-s'], 'icg');
    }

    async minify() {
        const doc = this.activeDoc(['ic10']);
        if (doc) await this.runTool(doc, ['minify'], 'ic10');
    }

    async disasm() {
        const doc = this.activeDoc(['ic10']);
        if (doc) await this.runTool(doc, ['disasm'], 'ic10');
    }

    reportMissingServer(err) {
        this.output.appendLine(`cannot start ic10c: ${err.message}`);
        this.output.appendLine('searched: icg.serverPath, workspace folders, parent directories, ~/go/bin, $GOPATH/bin, PATH');
        vscode.window
            .showWarningMessage(
                t(
                    'IC10 Go: cannot find the ic10c executable. Build it with "go build -o ic10c ./cmd/ic10c" or set "icg.serverPath".',
                    'IC10 Go: 找不到 ic10c 可执行文件。请用 "go build -o ic10c ./cmd/ic10c" 构建，或设置 "icg.serverPath"。'
                ),
                t('Open Settings', '打开设置')
            )
            .then((choice) => {
                if (choice === 'Open Settings' || choice === '打开设置') {
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
        // Send the whole document on each (debounced) change: full sync works
        // with every ic10c version, and .icg files are tiny, so it is not worth
        // depending on incremental-sync support.
        this.pendingChanges.set(e.document.uri.toString(), e.document.getText());
        clearTimeout(this.changeTimer);
        this.changeTimer = setTimeout(() => this.flushChanges(), 150);
    }

    flushChanges() {
        clearTimeout(this.changeTimer);
        this.changeTimer = undefined;
        if (!this.pendingChanges || this.pendingChanges.size === 0) return;
        for (const [uri, text] of this.pendingChanges) {
            this.notify('textDocument/didChange', {
                textDocument: { uri },
                contentChanges: [{ text }],
            });
        }
        this.pendingChanges.clear();
    }

    onClose(doc) {
        if (doc.languageId !== 'icg') return;
        this.pendingChanges.delete(doc.uri.toString());
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
                if (i.documentation) {
                    item.documentation = new vscode.MarkdownString(i.documentation.value || '');
                }
                return item;
            });
        } catch (err) {
            return [];
        }
    }

    // -- formatting / hover / definition -----------------------------------

    async formatting(doc) {
        if (!this.initialized) return [];
        try {
            const res = await this.request('textDocument/formatting', {
                textDocument: { uri: doc.uri.toString() },
                options: { tabSize: 4, insertSpaces: true },
            });
            if (!Array.isArray(res)) return [];
            return res.map(
                (e) =>
                    new vscode.TextEdit(
                        new vscode.Range(
                            e.range.start.line,
                            e.range.start.character,
                            e.range.end.line,
                            e.range.end.character
                        ),
                        e.newText
                    )
            );
        } catch (err) {
            return [];
        }
    }

    async hover(doc, pos) {
        if (!this.initialized) return undefined;
        try {
            const res = await this.request('textDocument/hover', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            if (!res || !res.contents) return undefined;
            return new vscode.Hover(new vscode.MarkdownString(res.contents.value));
        } catch (err) {
            return undefined;
        }
    }

    async definition(doc, pos) {
        if (!this.initialized) return undefined;
        try {
            const res = await this.request('textDocument/definition', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            if (!res) return undefined;
            const r = res.range;
            return new vscode.Location(
                vscode.Uri.parse(res.uri),
                new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character)
            );
        } catch (err) {
            return undefined;
        }
    }

    // -- navigation / symbols / actions ------------------------------------

    registerSemanticTokens(result) {
        const caps = (result && result.capabilities) || {};
        const provider = caps.semanticTokensProvider || {};
        const legend = provider.legend || { tokenTypes: [], tokenModifiers: [] };
        if (!legend.tokenTypes || legend.tokenTypes.length === 0) return;
        const legendObj = new vscode.SemanticTokensLegend(legend.tokenTypes, legend.tokenModifiers);
        this.disposables.push(
            vscode.languages.registerDocumentSemanticTokensProvider(
                'icg',
                { provideDocumentSemanticTokens: (doc) => this.semanticTokens(doc) },
                legendObj
            )
        );
    }

    async semanticTokens(doc) {
        try {
            const res = await this.request('textDocument/semanticTokens/full', {
                textDocument: { uri: doc.uri.toString() },
            });
            return new vscode.SemanticTokens(new Uint32Array((res && res.data) || []));
        } catch (err) {
            return undefined;
        }
    }

    async documentSymbols(doc) {
        try {
            const res = await this.request('textDocument/documentSymbol', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map(toDocumentSymbol);
        } catch (err) {
            return [];
        }
    }

    async foldingRanges(doc) {
        try {
            const res = await this.request('textDocument/foldingRange', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map(
                (r) =>
                    new vscode.FoldingRange(
                        r.startLine,
                        r.endLine,
                        r.kind === 'region' ? vscode.FoldingRangeKind.Region : undefined
                    )
            );
        } catch (err) {
            return [];
        }
    }

    async references(doc, pos) {
        try {
            const res = await this.request('textDocument/references', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            return (res || []).map(
                (l) => new vscode.Location(vscode.Uri.parse(l.uri), toRange(l.range))
            );
        } catch (err) {
            return [];
        }
    }

    async rename(doc, pos, newName) {
        const res = await this.request('textDocument/rename', {
            textDocument: { uri: doc.uri.toString() },
            position: { line: pos.line, character: pos.character },
            newName,
        });
        const edit = new vscode.WorkspaceEdit();
        const changes = (res && res.changes) || {};
        for (const [uri, edits] of Object.entries(changes)) {
            for (const e of edits) {
                edit.replace(vscode.Uri.parse(uri), toRange(e.range), e.newText);
            }
        }
        return edit;
    }

    async signatureHelp(doc, pos) {
        try {
            const res = await this.request('textDocument/signatureHelp', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            if (!res || !res.signatures) return undefined;
            const help = new vscode.SignatureHelp();
            help.signatures = res.signatures.map((s) => new vscode.SignatureInformation(s.label));
            help.activeSignature = res.activeSignature || 0;
            help.activeParameter = res.activeParameter || 0;
            return help;
        } catch (err) {
            return undefined;
        }
    }

    async codeActions(doc, range, context) {
        try {
            const diagnostics = (context.diagnostics || []).map((d) => ({
                range: fromRange(d.range),
                message: d.message,
                severity: d.severity,
                source: d.source,
            }));
            const res = await this.request('textDocument/codeAction', {
                textDocument: { uri: doc.uri.toString() },
                range: fromRange(range),
                context: { diagnostics },
            });
            return (res || []).map((a) => {
                const action = new vscode.CodeAction(a.title, vscode.CodeActionKind.QuickFix);
                const edit = new vscode.WorkspaceEdit();
                const changes = (a.edit && a.edit.changes) || {};
                for (const [uri, edits] of Object.entries(changes)) {
                    for (const e of edits) {
                        edit.replace(vscode.Uri.parse(uri), toRange(e.range), e.newText);
                    }
                }
                action.edit = edit;
                return action;
            });
        } catch (err) {
            return [];
        }
    }

    async inlayHints(doc) {
        try {
            const res = await this.request('textDocument/inlayHint', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map(
                (h) =>
                    new vscode.InlayHint(
                        new vscode.Position(h.position.line, h.position.character),
                        h.label
                    )
            );
        } catch (err) {
            return [];
        }
    }

    // -- JSON-RPC -----------------------------------------------------------

    request(method, params) {
        // Make sure the server has the latest edits before it answers.
        this.flushChanges();
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

function toRange(r) {
    return new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character);
}

function fromRange(r) {
    return {
        start: { line: r.start.line, character: r.start.character },
        end: { line: r.end.line, character: r.end.character },
    };
}

function toDocumentSymbol(s) {
    const sym = new vscode.DocumentSymbol(s.name, '', s.kind, toRange(s.range), toRange(s.selectionRange));
    if (s.children) sym.children = s.children.map(toDocumentSymbol);
    return sym;
}

// t picks a message based on the editor's display language.
function t(en, zh) {
    return (vscode.env.language || 'en').toLowerCase().startsWith('zh') ? zh : en;
}
