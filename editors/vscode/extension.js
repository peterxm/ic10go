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

// Oldest ic10c the extension is known to work with (build --json, current
// enum/logic-type tables). Older servers are reported after initialize.
const MIN_SERVER_VERSION = '0.6.4';

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
    context.subscriptions.push(
        vscode.commands.registerCommand('icg.graph', () => client.showCfg())
    );
    // Re-analyse open .icg files when any .icg file changes on disk, so editing
    // an imported file refreshes the documents that import it.
    const watcher = vscode.workspace.createFileSystemWatcher('**/*.icg');
    const refresh = () => client.refreshDependents();
    context.subscriptions.push(
        watcher,
        watcher.onDidChange(refresh),
        watcher.onDidCreate(refresh),
        watcher.onDidDelete(refresh)
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
        // Version of the running server we already auto-restarted from, so a
        // rebuilt ic10c binary is picked up at most once per stale version.
        this.autoRestartedFrom = '';
        this.output = vscode.window.createOutputChannel('IC10 Go');
        this.diags = vscode.languages.createDiagnosticCollection('icg');
        this.ic10Diags = vscode.languages.createDiagnosticCollection('ic10');
        this.disposables = [this.output, this.diags, this.ic10Diags];
        this.pendingChanges = new Map();
        this.changeTimer = undefined;
        this.statsByUri = new Map();
        this.status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
        this.status.command = 'icg.compile';
        this.disposables.push(this.status);
        this.disposables.push(
            vscode.window.onDidChangeActiveTextEditor(() => this.refreshStatus())
        );
        this.disposables.push(
            vscode.workspace.onDidOpenTextDocument((d) => this.onOpen(d))
        );
        this.disposables.push(
            vscode.workspace.onDidChangeTextDocument((e) => this.onChange(e))
        );
        this.disposables.push(
            vscode.workspace.onDidCloseTextDocument((d) => this.onClose(d))
        );
        // noCheck is applied when the server is spawned (via IC10C_NO_CHECK),
        // so a change to it needs a restart. The build/run flags are read live.
        this.disposables.push(
            vscode.workspace.onDidChangeConfiguration((e) => {
                if (!e.affectsConfiguration) return;
                if (
                    e.affectsConfiguration('icg.noCheck') ||
                    e.affectsConfiguration('icg.dynamicStack') ||
                    e.affectsConfiguration('icg.userStack') ||
                    e.affectsConfiguration('icg.redundantDeviceWrites') ||
                    e.affectsConfiguration('icg.mergeRenamedTails') ||
                    e.affectsConfiguration('icg.libDirs')
                ) {
                    this.restart();
                }
            })
        );
        this.disposables.push(
            vscode.languages.registerCompletionItemProvider(['icg', 'ic10'], {
                provideCompletionItems: (doc, pos) => this.completion(doc, pos),
                resolveCompletionItem: (item) => this.resolveCompletion(item),
            })
        );
        this.disposables.push(
            vscode.languages.registerDocumentFormattingEditProvider(['icg', 'ic10'], {
                provideDocumentFormattingEdits: (doc) => this.formatting(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerHoverProvider(['icg', 'ic10'], {
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
                prepareRename: (doc, pos) => this.prepareRename(doc, pos),
                provideRenameEdits: (doc, pos, newName) => this.rename(doc, pos, newName),
            })
        );
        this.disposables.push(
            vscode.languages.registerDocumentHighlightProvider('icg', {
                provideDocumentHighlights: (doc, pos) => this.documentHighlights(doc, pos),
            })
        );
        this.disposables.push(
            vscode.languages.registerSelectionRangeProvider('icg', {
                provideSelectionRanges: (doc, positions) => this.selectionRanges(doc, positions),
            })
        );
        this.disposables.push(
            vscode.languages.registerWorkspaceSymbolProvider({
                provideWorkspaceSymbols: (query) => this.workspaceSymbols(query),
            })
        );
        this.disposables.push(
            vscode.languages.registerCodeLensProvider('icg', {
                provideCodeLenses: (doc) => this.codeLenses(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerDocumentLinkProvider(['icg', 'ic10'], {
                provideDocumentLinks: (doc) => this.documentLinks(doc),
            })
        );
        this.disposables.push(
            vscode.languages.registerDocumentSymbolProvider('ic10', {
                provideDocumentSymbols: (doc) => this.ic10Symbols(doc),
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
        this.disposables.push(
            vscode.languages.registerColorProvider('icg', {
                provideDocumentColors: (doc) => this.documentColors(doc),
                provideColorPresentations: (doc, color) => this.colorPresentations(doc, color),
            })
        );
    }

    start() {
        const serverPath = this.resolveServer();
        this.output.appendLine(`starting language server: ${serverPath}`);

        try {
            const env = Object.assign({}, process.env);
            if (this.config().noCheck) env.IC10C_NO_CHECK = '1';
            this.applyStackEnv(env);
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
            // Do not leave the UI waiting on requests that will never be answered.
            for (const [, p] of this.pending) p.reject(new Error('language server exited'));
            this.pending.clear();
        });

        const folder = (vscode.workspace.workspaceFolders || [])[0];
        this.request('initialize', {
            processId: process.pid,
            rootUri: folder ? folder.uri.toString() : null,
            workspaceFolders: folder ? [{ uri: folder.uri.toString(), name: folder.name }] : null,
            locale: vscode.env.language,
            initializationOptions: {
                libDirs: this.resolvedLibDirs(),
            },
            capabilities: {
                textDocument: {
                    completion: {
                        completionItem: {
                            snippetSupport: false,
                            resolveSupport: { properties: ['documentation', 'detail'] },
                        },
                    },
                },
            },
        })
            .then((result) => {
                this.initialized = true;
                this.notify('initialized', {});
                this.registerSemanticTokens(result);
                const running = (result && result.serverInfo && result.serverInfo.version) || '';
                this.checkServerVersion(running);
                this.ensureCurrentBinary(running);
                for (const doc of vscode.workspace.textDocuments) {
                    if (doc.languageId === 'icg') this.sendDidOpen(doc);
                }
            })
            .catch((err) => this.output.appendLine(`initialize failed: ${err.message}`));
    }

    // ensureCurrentBinary restarts the server when the ic10c binary on disk is
    // newer than the process that answered initialize (for example after a
    // rebuild while the editor stayed open). Without this the stale process
    // keeps serving diagnostics from the old compiler.
    async ensureCurrentBinary(runningVersion) {
        if (this.autoRestartedFrom === runningVersion) return;
        const res = await this.execCli(['version']);
        const diskVersion = parseVersionFromVersionOutput(res.stdout);
        const running = parseVersion(runningVersion);
        const disk = parseVersion(diskVersion);
        if (!running || !disk) return;
        if (running[0] === disk[0] && running[1] === disk[1] && running[2] === disk[2]) return;
        this.autoRestartedFrom = runningVersion;
        this.output.appendLine(
            `running language server ${runningVersion} differs from on-disk ic10c ${diskVersion}; restarting`
        );
        this.restart();
    }

    // checkServerVersion warns when ic10c is older than MIN_SERVER_VERSION.
    checkServerVersion(version) {
        const got = parseVersion(version);
        const min = parseVersion(MIN_SERVER_VERSION);
        if (got && !versionLess(got, min)) return;
        this.output.appendLine(
            `ic10c ${version || '(unknown)'} is older than the required ${MIN_SERVER_VERSION}`
        );
        vscode.window.showWarningMessage(
            t(
                `IC10 Go: ic10c ${version || '(unknown)'} is older than the required ${MIN_SERVER_VERSION}; some features (e.g. compile) may not work. Please update ic10c.`,
                `IC10 Go: ic10c ${version || '(未知)'} 低于所需版本 ${MIN_SERVER_VERSION}，部分功能（如编译）可能不可用，请升级 ic10c。`
            )
        );
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
            dynamicStack: c.get('dynamicStack'),
            userStack: c.get('userStack'),
            redundantDeviceWrites: c.get('redundantDeviceWrites'),
            mergeRenamedTails: c.get('mergeRenamedTails'),
            autoTable: c.get('autoTable'),
            jumpTable: c.get('jumpTable'),
            relJump: c.get('relJump'),
            fast: c.get('fast'),
            unsafe: c.get('unsafe'),
            dataLayout: c.get('dataLayout'),
            dataAccess: c.get('dataAccess'),
            maxLines: c.get('maxLines'),
            maxBytes: c.get('maxBytes'),
            maxLine: c.get('maxLine'),
            runSteps: c.get('runSteps'),
            runTrace: c.get('runTrace'),
            runSet: c.get('runSet'),
        };
    }

    // applyStackEnv passes the stack settings to ic10c through the environment,
    // so the LSP and every CLI invocation use the same user-stack boundary.
    applyStackEnv(env) {
        const cfg = this.config();
        if (cfg.dynamicStack === true) env.IC10C_DYNAMIC_STACK = '1';
        if (cfg.userStack && cfg.userStack > 0) env.IC10C_USER_STACK = String(cfg.userStack);
        if (cfg.redundantDeviceWrites === true) env.IC10C_REDUNDANT_DEVICE_WRITES = '1';
        if (cfg.mergeRenamedTails === true) env.IC10C_MERGE_RENAMED_TAILS = '1';
        if (cfg.maxLines > 0) env.IC10C_MAX_LINES = String(cfg.maxLines);
        if (cfg.maxBytes > 0) env.IC10C_MAX_BYTES = String(cfg.maxBytes);
        if (cfg.maxLine > 0) env.IC10C_MAX_LINE = String(cfg.maxLine);
    }

    // buildFlags returns the shared ic10c build/run options from the settings.
    buildFlags(cfg) {
        const flags = [];
        if (cfg.stableIns) flags.push('--stable-ins');
        if (cfg.autoTable) flags.push('--auto-table');
        if (cfg.jumpTable) flags.push('--jump-table');
        if (cfg.relJump) flags.push('--rel-jump');
        if (cfg.fast) flags.push('--fast');
        if (cfg.unsafe) flags.push('--unsafe');
        if (cfg.dataLayout && cfg.dataLayout !== 'top') flags.push('--data-layout', cfg.dataLayout);
        if (cfg.dataAccess && cfg.dataAccess !== 'get') flags.push('--data-access', cfg.dataAccess);
        flags.push(...this.libArgs());
        return flags;
    }

    // libArgs returns the `--lib DIR` flags for the configured import dirs.
    libArgs() {
        const args = [];
        for (const dir of this.resolvedLibDirs()) args.push('--lib', dir);
        return args;
    }

    // resolvedLibDirs returns icg.libDirs with relative entries resolved against
    // the first workspace folder, for the language server and CLI invocations.
    resolvedLibDirs() {
        const dirs = vscode.workspace.getConfiguration('icg').get('libDirs') || [];
        const folder = (vscode.workspace.workspaceFolders || [])[0];
        return dirs
            .filter((d) => typeof d === 'string' && d.length > 0)
            .map((d) => (folder && !path.isAbsolute(d) ? path.join(folder.uri.fsPath, d) : d));
    }

    // execCli runs the ic10c binary with the given arguments.
    execCli(args) {
        const serverPath = this.resolveServer();
        const env = Object.assign({}, process.env);
        if (this.config().noCheck) env.IC10C_NO_CHECK = '1';
        this.applyStackEnv(env);
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
    // It prefers the document's own directory so relative `import`s resolve the
    // same way they do in the editor, falling back to the OS temp dir for
    // untitled or read-only documents.
    async withTempFile(doc, fn) {
        const name = `icg-${process.pid}-${Date.now()}.icg`;
        let tmp = path.join(os.tmpdir(), name);
        const onDisk = doc.uri && doc.uri.scheme === 'file' && doc.fileName;
        if (onDisk) {
            const beside = path.join(path.dirname(doc.fileName), `.${name}`);
            try {
                fs.writeFileSync(beside, doc.getText(), 'utf8');
                tmp = beside;
            } catch (err) {
                fs.writeFileSync(tmp, doc.getText(), 'utf8');
            }
        } else {
            fs.writeFileSync(tmp, doc.getText(), 'utf8');
        }
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

    // compile runs `ic10c build --json` and previews the result. When the
    // program uses a data table it also copies the one-time loader to the
    // clipboard, so a single command covers the whole "compile -> paste into
    // the chip" flow for non-programmers.
    async compile() {
        const doc = this.activeICG();
        if (!doc) return;
        await this.withTempFile(doc, async (tmp) => {
            const buildArgs = ['build', '--json', ...this.buildFlags(this.config()), tmp];
            const build = await this.execCli(buildArgs);

            let result;
            try {
                result = JSON.parse(build.stdout);
            } catch (err) {
                this.output.appendLine(
                    `=== compile failed: ${path.basename(doc.fileName)} ===\n${build.stderr || build.stdout}`
                );
                this.output.show(true);
                vscode.window.showErrorMessage(
                    t('IC10 Go: compilation failed. See the "IC10 Go" output.', 'IC10 Go: 编译失败，详见 "IC10 Go" 输出面板。')
                );
                return;
            }
            if (!result.ok) {
                this.output.appendLine(`=== compile failed: ${path.basename(doc.fileName)} ===`);
                for (const d of result.diagnostics || []) {
                    const at = d.range && d.range.start ? `${d.range.start.line}:${d.range.start.col}` : '?';
                    const code = d.code ? ` [${d.code}]` : '';
                    this.output.appendLine(`${at} ${d.severity}: ${d.message}${code}`);
                }
                this.output.show(true);
                vscode.window.showErrorMessage(
                    t('IC10 Go: compilation failed. See the "IC10 Go" output.', 'IC10 Go: 编译失败，详见 "IC10 Go" 输出面板。')
                );
                return;
            }

            // Multi-chip: pick which chip to preview / install. A single chip
            // block is mirrored by the top-level result, so no picker is needed.
            let chosen = result;
            const chips = result.chips || [];
            if (chips.length > 1) {
                const pick = await vscode.window.showQuickPick(
                    chips.map((c) => ({
                        label: c.name,
                        description: `${(c.stats || {}).lines || 0} lines`,
                        chip: c,
                    })),
                    { placeHolder: t('Select a chip to install', '选择要安装的芯片') }
                );
                if (!pick) return;
                chosen = {
                    code: pick.chip.code,
                    stats: pick.chip.stats,
                    limits: result.limits,
                    data: {
                        needed: !!pick.chip.loader,
                        loader: pick.chip.loader,
                        setup: pick.chip.setup,
                    },
                };
            }

            const preview = await vscode.workspace.openTextDocument({
                content: chosen.code,
                language: 'ic10',
            });
            await vscode.window.showTextDocument(preview, {
                viewColumn: vscode.ViewColumn.Beside,
                preview: true,
            });

            const st = chosen.stats || {};
            const lim = chosen.limits || {};
            const budget =
                `${st.lines || 0}/${lim.lines || 0} lines · ${st.bytes || 0}/${lim.bytes || 0} B · ` +
                `${st.maxLine || 0}/${lim.maxLine || 0} ch · ${st.regs || 0}/${lim.regs || 0} reg`;
            this.output.appendLine(`=== ${path.basename(doc.fileName)} ===\n${budget}`);

            const data = chosen.data || {};
            const loaders =
                data.loaders && data.loaders.length
                    ? data.loaders
                    : data.loader && data.loader.trim()
                      ? [data.loader]
                      : [];
            const copyRuntime = t('Copy runtime code', '复制运行代码');
            const copyRuntimeNow = async () => {
                await vscode.env.clipboard.writeText(chosen.code);
                vscode.window.setStatusBarMessage(t('IC10 Go: runtime code copied', 'IC10 Go: 运行代码已复制'), 5000);
            };
            if (data.needed && loaders.length === 1) {
                await vscode.env.clipboard.writeText(loaders[0]);
                const pick = await vscode.window.showInformationMessage(
                    t('IC10 Go: this program needs a one-time install loader (data table and/or hoisted setup writes). It is on your clipboard — paste it into the IC chip and run it once, then paste the runtime code from the preview on the right.',
                        'IC10 Go: 该程序需要一次性「安装代码」（数据表和/或外提的设置写入）。已复制到剪贴板：先粘贴到 IC 芯片并运行一次，再用右侧预览中的「运行代码」覆盖它。'),
                    copyRuntime);
                if (pick === copyRuntime) await copyRuntimeNow();
            } else if (data.needed && loaders.length > 1) {
                const next = t('Next chunk', '下一块');
                for (let i = 0; i < loaders.length; i++) {
                    await vscode.env.clipboard.writeText(loaders[i]);
                    const last = i + 1 === loaders.length;
                    const pick = await vscode.window.showInformationMessage(
                        t(`IC10 Go: install loader chunk ${i + 1}/${loaders.length} is on your clipboard — paste it into the IC chip and run it, then continue.`,
                            `IC10 Go: 安装代码第 ${i + 1}/${loaders.length} 块已复制——粘贴到 IC 芯片运行后继续。`),
                        last ? copyRuntime : next);
                    if (!last && pick !== next) return;
                    if (last && pick === copyRuntime) await copyRuntimeNow();
                }
            } else {
                vscode.window.setStatusBarMessage(`IC10 Go: ${st.lines || 0}/${lim.lines || 0} lines`, 5000);
            }
        });
    }

    // run compiles and executes the document in the built-in VM.
    async run() {
        const doc = this.activeICG();
        if (!doc) return;
        await this.withTempFile(doc, async (tmp) => {
            const args = ['run'];
            if (this.config().stableIns) args.push('--stable-ins');
            const steps = this.config().runSteps;
            if (steps && steps > 0) args.push('--steps', String(steps));
            if (this.config().runTrace) args.push('--trace');
            for (const s of this.config().runSet || []) {
                if (s) args.push('--set', s);
            }
            args.push(...this.libArgs());
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

    // showCfg renders the control-flow graph (Mermaid) in the Markdown preview.
    async showCfg() {
        const doc = this.activeICG();
        if (!doc) return;
        await this.withTempFile(doc, async (tmp) => {
            const res = await this.execCli(['graph', ...this.libArgs(), tmp]);
            if (res.code !== 0) {
                this.output.appendLine(`=== graph failed ===\n${res.stderr}`);
                this.output.show(true);
                vscode.window.showErrorMessage(
                    t('IC10 Go: graph failed. See the "IC10 Go" output.', 'IC10 Go: 生成控制流图失败，详见 "IC10 Go" 输出面板。')
                );
                return;
            }
            const md = '```mermaid\n' + res.stdout + '```\n';
            const preview = await vscode.workspace.openTextDocument({
                content: md,
                language: 'markdown',
            });
            await vscode.window.showTextDocument(preview, {
                viewColumn: vscode.ViewColumn.Beside,
                preview: true,
            });
            await vscode.commands.executeCommand('markdown.showPreview', preview.uri);
        });
    }

    // -- native IC10 (.ic / .ic10) -----------------------------------------

    // checkIC10 reports the IC10 editor limits (icg.maxLines / icg.maxBytes /
    // icg.maxLine, default 128 lines / 4096 bytes / 90 chars). Native IC10 has
    // no full validator yet, so this is what we can check without the game's
    // assembler.
    checkIC10(doc) {
        const cfg = this.config();
        const maxLines = cfg.maxLines > 0 ? cfg.maxLines : 128;
        const maxBytes = cfg.maxBytes > 0 ? cfg.maxBytes : 4096;
        const maxLine = cfg.maxLine > 0 ? cfg.maxLine : 90;
        const text = doc.getText();
        const lines = text.split('\n');
        const bytes = Buffer.byteLength(text, 'utf8');
        const diags = [];
        if (bytes > maxBytes) {
            diags.push(
                this.ic10Diag(
                    new vscode.Range(0, 0, 0, 0),
                    t(`program is ${bytes} bytes, exceeding the ${maxBytes}-byte limit`, `程序为 ${bytes} 字节，超过 ${maxBytes} 字节上限`)
                )
            );
        }
        if (lines.length > maxLines) {
            diags.push(
                this.ic10Diag(
                    new vscode.Range(0, 0, 0, 0),
                    t(`program has ${lines.length} lines, exceeding the ${maxLines}-line limit`, `程序有 ${lines.length} 行，超过 ${maxLines} 行上限`)
                )
            );
        }
        lines.forEach((ln, i) => {
            const n = ln.length;
            if (n > maxLine) {
                diags.push(
                    this.ic10Diag(
                        new vscode.Range(i, maxLine, i, n),
                        t(`line is ${n} characters, exceeding the ${maxLine}-character limit`, `该行 ${n} 个字符，超过 ${maxLine} 字符上限`)
                    )
                );
            }
        });
        this.ic10Diags.set(doc.uri, diags);
    }

    ic10Diag(range, message) {
        const d = new vscode.Diagnostic(range, message, vscode.DiagnosticSeverity.Error);
        d.source = 'ic10c';
        d.code = 'ic10-limit';
        return d;
    }

    // ic10Symbols lists the labels of a native IC10 program.
    ic10Symbols(doc) {
        const symbols = [];
        const lines = doc.getText().split('\n');
        for (let i = 0; i < lines.length; i++) {
            const m = /^\s*([A-Za-z_][A-Za-z0-9_]*):/.exec(lines[i]);
            if (!m) continue;
            const start = lines[i].indexOf(m[1]);
            symbols.push(
                new vscode.DocumentSymbol(
                    m[1],
                    'label',
                    vscode.SymbolKind.Key,
                    new vscode.Range(i, 0, i, lines[i].length),
                    new vscode.Range(i, start, i, start + m[1].length)
                )
            );
        }
        return symbols;
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
        if (doc.languageId === 'icg') {
            if (this.initialized) this.sendDidOpen(doc, 'icg');
            return;
        }
        if (doc.languageId === 'ic10') {
            this.checkIC10(doc);
            if (this.initialized) this.sendDidOpen(doc, 'ic10');
        }
    }

    onChange(e) {
        if (e.document.languageId === 'ic10') {
            this.checkIC10(e.document);
        } else if (e.document.languageId !== 'icg') {
            return;
        }
        if (!this.initialized) return;
        // Send the whole document on each (debounced) change: full sync works
        // with every ic10c version, and the files are tiny, so it is not worth
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

    // Re-analyse every open .icg file when an .icg file changes on disk, so an
    // edit to an imported file refreshes the documents that import it.
    refreshDependents() {
        if (!this.initialized) return;
        for (const doc of vscode.workspace.textDocuments) {
            if (doc.languageId !== 'icg') continue;
            this.pendingChanges.set(doc.uri.toString(), doc.getText());
        }
        this.flushChanges();
    }

    onClose(doc) {
        if (doc.languageId === 'ic10') {
            this.ic10Diags.delete(doc.uri);
            this.diags.delete(doc.uri);
            this.pendingChanges.delete(doc.uri.toString());
            if (this.initialized) {
                this.notify('textDocument/didClose', {
                    textDocument: { uri: doc.uri.toString() },
                });
            }
            return;
        }
        if (doc.languageId !== 'icg') return;
        this.pendingChanges.delete(doc.uri.toString());
        this.statsByUri.delete(doc.uri.toString());
        this.diags.delete(doc.uri);
        this.refreshStatus();
        if (this.initialized) {
            this.notify('textDocument/didClose', {
                textDocument: { uri: doc.uri.toString() },
            });
        }
    }

    sendDidOpen(doc, languageId = 'icg') {
        this.notify('textDocument/didOpen', {
            textDocument: {
                uri: doc.uri.toString(),
                languageId,
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
                // LSP CompletionItemKind is 1-based; VSCode's is 0-based.
                const kind = i.kind ? i.kind - 1 : vscode.CompletionItemKind.Text;
                const item = new vscode.CompletionItem(i.label, kind);
                if (i.detail) item.detail = i.detail;
                if (i.documentation) {
                    item.documentation = new vscode.MarkdownString(i.documentation.value || '');
                }
                // A textEdit replaces a range (used for completions inside strings).
                if (i.textEdit) {
                    const r = i.textEdit.range;
                    item.range = new vscode.Range(
                        r.start.line,
                        r.start.character,
                        r.end.line,
                        r.end.character
                    );
                    if (i.textEdit.newText) item.insertText = i.textEdit.newText;
                }
                // Keep the resolve payload so documentation can be fetched lazily.
                item._data = i.data;
                return item;
            });
        } catch (err) {
            return [];
        }
    }

    // resolveCompletion fetches the documentation for a completion item.
    async resolveCompletion(item) {
        try {
            const res = await this.request('completionItem/resolve', {
                label: item.label,
                data: item._data,
            });
            if (res) {
                if (res.detail) item.detail = res.detail;
                if (res.documentation) {
                    item.documentation = new vscode.MarkdownString(res.documentation.value || '');
                }
            }
        } catch (err) {
            // keep the unresolved item
        }
        return item;
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
                {
                    provideDocumentSemanticTokens: (doc) => this.semanticTokens(doc),
                    provideDocumentRangeSemanticTokens: (doc, range) => this.semanticTokensRange(doc, range),
                },
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

    async semanticTokensRange(doc, range) {
        try {
            const res = await this.request('textDocument/semanticTokens/range', {
                textDocument: { uri: doc.uri.toString() },
                range: fromRange(range),
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
                context: { includeDeclaration: true },
            });
            return (res || []).map(
                (l) => new vscode.Location(vscode.Uri.parse(l.uri), toRange(l.range))
            );
        } catch (err) {
            return [];
        }
    }

    async prepareRename(doc, pos) {
        try {
            const res = await this.request('textDocument/prepareRename', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            if (!res || !res.range) return undefined;
            return { range: toRange(res.range), placeholder: res.placeholder };
        } catch (err) {
            return undefined;
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

    async documentHighlights(doc, pos) {
        try {
            const res = await this.request('textDocument/documentHighlight', {
                textDocument: { uri: doc.uri.toString() },
                position: { line: pos.line, character: pos.character },
            });
            return (res || []).map((h) => new vscode.DocumentHighlight(toRange(h.range), h.kind));
        } catch (err) {
            return [];
        }
    }

    async selectionRanges(doc, positions) {
        try {
            const res = await this.request('textDocument/selectionRange', {
                textDocument: { uri: doc.uri.toString() },
                positions: positions.map((p) => ({ line: p.line, character: p.character })),
            });
            return (res || []).map(toSelectionRange);
        } catch (err) {
            return [];
        }
    }

    async workspaceSymbols(query) {
        try {
            const res = await this.request('workspace/symbol', { query: query || '' });
            return (res || []).map(
                (s) =>
                    new vscode.SymbolInformation(
                        s.name,
                        Math.max(0, (s.kind || 1) - 1),
                        '',
                        new vscode.Location(vscode.Uri.parse(s.location.uri), toRange(s.location.range))
                    )
            );
        } catch (err) {
            return [];
        }
    }

    async codeLenses(doc) {
        try {
            const res = await this.request('textDocument/codeLens', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map((l) => {
                const cmd = l.command
                    ? new vscode.Command(l.command.title, l.command.command, l.command.arguments)
                    : undefined;
                return new vscode.CodeLens(toRange(l.range), cmd);
            });
        } catch (err) {
            return [];
        }
    }

    async documentLinks(doc) {
        try {
            const res = await this.request('textDocument/documentLink', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map(
                (l) => new vscode.DocumentLink(toRange(l.range), l.target ? vscode.Uri.parse(l.target) : undefined)
            );
        } catch (err) {
            return [];
        }
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
                action.isPreferred = !!a.isPreferred;
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

    async documentColors(doc) {
        try {
            const res = await this.request('textDocument/documentColor', {
                textDocument: { uri: doc.uri.toString() },
            });
            return (res || []).map(
                (c) =>
                    new vscode.ColorInformation(
                        toRange(c.range),
                        new vscode.Color(c.color.red, c.color.green, c.color.blue, c.color.alpha)
                    )
            );
        } catch (err) {
            return [];
        }
    }

    async colorPresentations(doc, color) {
        try {
            const res = await this.request('textDocument/colorPresentation', {
                textDocument: { uri: doc.uri.toString() },
                color: { red: color.red, green: color.green, blue: color.blue, alpha: color.alpha },
            });
            return (res || []).map((p) => new vscode.ColorPresentation(p.label));
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
            // A hung or crashed server must not leave callers waiting forever.
            const timer = setTimeout(() => {
                this.pending.delete(id);
                reject(new Error('language server request timed out: ' + method));
            }, 15000);
            this.pending.set(id, {
                resolve: (v) => {
                    clearTimeout(timer);
                    resolve(v);
                },
                reject: (e) => {
                    clearTimeout(timer);
                    reject(e);
                },
            });
            this.send({ jsonrpc: '2.0', id, method, params });
        });
    }

    notify(method, params) {
        this.send({ jsonrpc: '2.0', method, params });
    }

    // tracing reports the icg.trace.server setting.
    tracing() {
        return vscode.workspace.getConfiguration('icg').get('trace.server') || 'off';
    }

    // traceMessage appends an LSP message to the output channel when tracing is on.
    traceMessage(dir, obj) {
        if (this.tracing() === 'off') return;
        const stamp = new Date().toISOString().substr(11, 12);
        this.output.appendLine(`[Trace - ${stamp}] ${dir} ${JSON.stringify(obj)}`);
    }

    send(obj) {
        if (!this.proc || !this.proc.stdin.writable) return;
        this.traceMessage('-->', obj);
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
            this.traceMessage('<--', msg);
            this.handle(msg);
        }
    }

    handle(msg) {
        if (msg.id !== undefined && msg.method === undefined) {
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
            return;
        }
        if (msg.method === 'icg/stats') {
            this.onStats(msg.params);
            return;
        }
        // Requests initiated by the server. Reply so the server never waits
        // forever; handle the handful of methods a minimal client must answer.
        if (msg.id !== undefined && msg.method !== undefined) {
            this.handleServerRequest(msg);
        }
    }

    handleServerRequest(msg) {
        const params = msg.params || {};
        switch (msg.method) {
            case 'window/showMessage':
                this.showServerMessage(params);
                this.send({ jsonrpc: '2.0', id: msg.id, result: null });
                return;
            case 'window/logMessage':
                this.output.appendLine(params.message || '');
                this.send({ jsonrpc: '2.0', id: msg.id, result: null });
                return;
            case 'workspace/configuration': {
                // No server-side settings yet: answer null for every item.
                const items = params.items || [];
                this.send({ jsonrpc: '2.0', id: msg.id, result: items.map(() => null) });
                return;
            }
            default:
                this.send({
                    jsonrpc: '2.0',
                    id: msg.id,
                    error: { code: -32601, message: 'method not found: ' + msg.method },
                });
        }
    }

    showServerMessage(params) {
        const text = params.message || '';
        switch (params.type) {
            case 1:
                vscode.window.showErrorMessage(text);
                break;
            case 2:
                vscode.window.showWarningMessage(text);
                break;
            default:
                vscode.window.showInformationMessage(text);
        }
    }

    onStats(p) {
        if (p.error) this.statsByUri.delete(p.uri);
        else this.statsByUri.set(p.uri, p);
        this.refreshStatus();
    }

    refreshStatus() {
        const editor = vscode.window.activeTextEditor;
        if (!editor || editor.document.languageId !== 'icg') {
            this.status.hide();
            return;
        }
        const p = this.statsByUri.get(editor.document.uri.toString());
        if (!p) {
            this.status.hide();
            return;
        }
        let text = t(
            `IC10: ${p.lines}/${p.maxLines} lines · ${p.bytes}/${p.maxBytes} B · ${p.maxLineLen}/${p.maxLineMax} ch · ${p.regs}/${p.maxRegs} reg`,
            `IC10: ${p.lines}/${p.maxLines} 行 · ${p.bytes}/${p.maxBytes} 字节 · ${p.maxLineLen}/${p.maxLineMax} 字符 · ${p.regs}/${p.maxRegs} 寄存器`
        );
        if (p.dataBase !== undefined) {
            text += ` · data ${p.dataBase}..${p.dataEnd}`;
        }
        if (p.stackUser !== undefined) {
            const stack = p.stackUnbounded
                ? t('unbounded', '无界')
                : `${p.stackUser}/${p.stackUserLimit}`;
            text += t(` · stack ${stack}`, ` · 栈 ${stack}`);
        }
        if (p.chips) {
            text += t(` · ${p.chips} chips`, ` · ${p.chips} 块芯片`);
        }
        if (p.loaderLines) {
            text += t(` · loader ${p.loaderLines}`, ` · 装载器 ${p.loaderLines}`);
        }
        this.status.text = text;
        const tips = [t('IC10 budget — click to compile', 'IC10 预算 — 点击编译')];
        if (p.autoTabled) {
            tips.push(t(`${p.autoTabled} switch(es) auto-tabled; reinstall the data loader`,
                `已自动表化 ${p.autoTabled} 处 switch；请重装数据段`));
        }
        if (p.loaderLines) {
            tips.push(t('needs a one-time loader: install it on the IC and run it once before the main code',
                '需要一次性装载器：先装到 IC 上运行一次，再装主代码'));
        }
        if (p.dataWarn) tips.push(p.dataWarn);
        if (p.stackUnbounded) tips.push(t('push depth is unbounded', 'push 深度无界'));
        if (p.stackUser !== undefined) {
            const mode = p.stackDynamic ? t('dynamic', '动态') : t('fixed', '固定');
            const max = p.stackUserMax ? t(` · highest slot ${p.stackUserMax - 1}`, ` · 最高槽位 ${p.stackUserMax - 1}`) : '';
            tips.push(t(
                `stack user ${p.stackUser}/${p.stackUserLimit} (${mode}${max}; push ${p.stackPush}, manual ${p.stackManual}) · compiler ${p.stackCompiler} at [${p.stackCompilerBase}..511] (data ${p.stackData}, spills ${p.stackSpills})`,
                `栈 用户 ${p.stackUser}/${p.stackUserLimit}（${mode}${max}；push ${p.stackPush}，手动 ${p.stackManual}）· 编译器 ${p.stackCompiler} @ [${p.stackCompilerBase}..511]（data ${p.stackData}，溢出 ${p.stackSpills}）`
            ));
        }
        this.status.tooltip = tips.join('\n');
        this.status.show();
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
            if (d.code) diag.code = d.code;
            if (d.codeDescription && d.codeDescription.href) {
                diag.codeDescription = { href: vscode.Uri.parse(d.codeDescription.href) };
            }
            if (d.tags) diag.tags = d.tags;
            if (d.relatedInformation) {
                diag.relatedInformation = d.relatedInformation.map(
                    (ri) =>
                        new vscode.DiagnosticRelatedInformation(
                            new vscode.Location(vscode.Uri.parse(ri.location.uri), toRange(ri.location.range)),
                            ri.message
                        )
                );
            }
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

// parseVersion extracts major.minor.patch from a version string like "0.6.4"
// or "0.6.4+abc1234". Returns undefined when it cannot be parsed.
function parseVersion(v) {
    const m = /^(\d+)\.(\d+)\.(\d+)/.exec(String(v || ''));
    if (!m) return undefined;
    return [Number(m[1]), Number(m[2]), Number(m[3])];
}

// parseVersionFromVersionOutput extracts the version from `ic10c version`
// output (its first line is "ic10c X.Y.Z").
function parseVersionFromVersionOutput(out) {
    const m = /ic10c\s+(\S+)/.exec(String(out || ''));
    return m ? m[1] : '';
}

// versionLess reports whether a < b for [major, minor, patch] tuples.
function versionLess(a, b) {
    for (let i = 0; i < 3; i++) {
        if (a[i] !== b[i]) return a[i] < b[i];
    }
    return false;
}

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
    // LSP SymbolKind is 1-based; VSCode's is 0-based.
    const sym = new vscode.DocumentSymbol(
        s.name,
        '',
        Math.max(0, (s.kind || 1) - 1),
        toRange(s.range),
        toRange(s.selectionRange)
    );
    if (s.children) sym.children = s.children.map(toDocumentSymbol);
    return sym;
}

function toSelectionRange(s) {
    const r = new vscode.SelectionRange(toRange(s.range));
    if (s.parent) r.parent = toSelectionRange(s.parent);
    return r;
}

// t picks a message based on the editor's display language.
function t(en, zh) {
    return (vscode.env.language || 'en').toLowerCase().startsWith('zh') ? zh : en;
}
