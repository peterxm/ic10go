// Smoke test for the extension: loads extension.js with a mocked `vscode` and
// runs activate(), so constructor/registration errors are caught without VSCode.
//
//   node editors/vscode/smoke.js
const Module = require('module');
const path = require('path');

function disposable() {
    return { dispose() {} };
}

const noop = () => {};

// Commands the extension registers; a missing one fails the smoke test.
const registeredCommands = [];

const mock = {
    StatusBarAlignment: { Left: 1, Right: 2 },
    window: {
        createOutputChannel: () => ({ appendLine: noop, append: noop, show: noop, dispose: noop }),
        createStatusBarItem: () => ({ show: noop, hide: noop, dispose: noop }),
        createTextEditorDecorationType: () => disposable(),
        visibleTextEditors: [],
        registerTreeDataProvider: () => disposable(),
        createWebviewPanel: () => ({
            reveal: noop,
            dispose: noop,
            webview: {
                html: '',
                cspSource: 'vscode-webview://test',
                postMessage: () => Promise.resolve(true),
                onDidReceiveMessage: disposable,
            },
            onDidDispose: disposable,
        }),
        showOpenDialog: () => Promise.resolve([]),
        showInputBox: () => Promise.resolve(undefined),
        showQuickPick: () => Promise.resolve(undefined),
        onDidChangeActiveTextEditor: disposable,
        activeTextEditor: undefined,
        showWarningMessage: () => Promise.resolve(undefined),
        showErrorMessage: () => Promise.resolve(undefined),
        setStatusBarMessage: () => disposable(),
        showTextDocument: () => Promise.resolve({}),
    },
    languages: {
        createDiagnosticCollection: () => ({ set: noop, delete: noop, clear: noop, dispose: noop }),
        registerCompletionItemProvider: disposable,
        registerDocumentFormattingEditProvider: disposable,
        registerHoverProvider: disposable,
        registerDefinitionProvider: disposable,
        registerDocumentSymbolProvider: disposable,
        registerFoldingRangeProvider: disposable,
        registerReferenceProvider: disposable,
        registerRenameProvider: disposable,
        registerDocumentHighlightProvider: disposable,
        registerSelectionRangeProvider: disposable,
        registerWorkspaceSymbolProvider: disposable,
        registerCodeLensProvider: disposable,
        registerDocumentLinkProvider: disposable,
        registerSignatureHelpProvider: disposable,
        registerCodeActionsProvider: disposable,
        registerInlayHintsProvider: disposable,
        registerColorProvider: disposable,
        registerDocumentSemanticTokensProvider: disposable,
    },
    workspace: {
        onDidOpenTextDocument: disposable,
        onDidChangeTextDocument: disposable,
        onDidCloseTextDocument: disposable,
        onDidSaveTextDocument: disposable,
        onDidChangeConfiguration: disposable,
        getConfiguration: () => ({ get: (k) => (k === 'bench.autoConnect' ? false : undefined) }),
        createFileSystemWatcher: () => ({
            onDidChange: disposable,
            onDidCreate: disposable,
            onDidDelete: disposable,
            dispose: noop,
        }),
        workspaceFolders: [],
        textDocuments: [],
        openTextDocument: () => Promise.resolve({}),
    },
    commands: {
        registerCommand: (name) => {
            registeredCommands.push(name);
            return disposable();
        },
        executeCommand: noop,
    },
    env: { language: 'zh-cn', clipboard: { writeText: () => Promise.resolve() } },
    Uri: { parse: (s) => s },
    Range: class {},
    Position: class {},
    Selection: class {},
    CompletionItem: class {},
    MarkdownString: class {},
    Hover: class {},
    Location: class {},
    DocumentSymbol: class {},
    FoldingRange: class {},
    FoldingRangeKind: { Region: 1 },
    SignatureHelp: class {},
    SignatureInformation: class {},
    CodeAction: class {},
    CodeActionKind: { QuickFix: 'quickfix' },
    WorkspaceEdit: class { replace() {} },
    InlayHint: class {},
    Color: class {},
    ColorInformation: class {},
    ColorPresentation: class {},
    DiagnosticRelatedInformation: class {},
    SemanticTokens: class {},
    SemanticTokensLegend: class {},
    Diagnostic: class {},
    DiagnosticSeverity: { Error: 1, Warning: 2, Information: 3 },
    ViewColumn: { Active: 1, Beside: 2 },
    TreeItemCollapsibleState: { None: 0, Collapsed: 1, Expanded: 2 },
    TreeItem: class {
        constructor(label, collapsibleState) {
            this.label = label;
            this.collapsibleState = collapsibleState;
        }
    },
    ThemeIcon: class {
        constructor(id, color) {
            this.id = id;
            this.color = color;
        }
    },
    ThemeColor: class {
        constructor(id) {
            this.id = id;
        }
    },
    EventEmitter: class {
        constructor() {
            this.event = () => disposable();
        }
        fire() {}
    },
};

const fakeProc = {
    stdin: { writable: true, write: noop },
    stdout: { on: noop },
    stderr: { on: noop },
    on: noop,
    kill: noop,
};

const originalLoad = Module._load;
Module._load = function (request, parent, isMain) {
    if (request === 'vscode') return mock;
    if (request === 'child_process') return { spawn: () => fakeProc, execFile: noop };
    return originalLoad.call(this, request, parent, isMain);
};

const ext = require(path.join(__dirname, 'extension.js'));
ext.activate({ subscriptions: [], workspaceState: { get: () => undefined, update: () => Promise.resolve() } });

// Panel HTML builders must render sample payloads without throwing.
const { Bench } = require(path.join(__dirname, 'bench.js'));
const bench = new Bench({ output: { appendLine: noop }, execCli: noop, config: () => ({}), buildFlags: () => [], activeICG: () => undefined, activeProgram: () => undefined });
const compare = bench.compareHtml();
if (!compare.includes('id="cols"') || !compare.includes('id="diffOnly"') || !compare.includes('spbar') || !compare.includes('acquireVsCodeApi')) {
    console.error('compareHtml is missing its render target/script');
    process.exit(1);
}
const ports = bench.portsHtml({
    chip: { name: 'A' },
    lookups: [{ deviceIndex: 0, networkIndex: 0, name: 'Battery', prefab: 'Battery_Wireless_cell', logic: { Setting: 1 } }],
});
if (!ports.includes('Battery_Wireless_cell') || !ports.includes('d0')) {
    console.error('portsHtml did not render the lookup row');
    process.exit(1);
}
const writes = bench.writesHtml(
    { writes: [{ seq: 1, device: 'Battery', logic: 'On', slot: -1, value: 1 }] },
    { 'Battery|On|-1': 0 }
);
if (!writes.includes('Battery') || !writes.includes('On') || !writes.includes('class="changed"')) {
    console.error('writesHtml did not render the write row / baseline diff');
    process.exit(1);
}
const chunks = bench.buildStackLoader([[0, 1], [5, 2.5], [509, 79464601]], 'get', 2);
if (chunks.length !== 2 || !chunks[0].startsWith('put db 0 1') || !chunks[1].includes('put db 509 79464601')) {
    console.error('buildStackLoader chunking/format wrong: ' + JSON.stringify(chunks));
    process.exit(1);
}
if (bench.buildStackLoader([[3, 0]], 'stack', 0)[0].trim() !== 'poke 3 0') {
    console.error('buildStackLoader did not use poke for stack access');
    process.exit(1);
}

// The connected tree node must use a Command object: a bare string made VSCode
// read command "undefined" and fail with "command 'undefined' not found".
bench.register({ subscriptions: [], workspaceState: { get: () => undefined, update: () => Promise.resolve() } });
bench.conn = {}; // pretend connected
bench.chips = [];
const roots = bench.tree.getChildren(undefined);
const connItem = roots[0];
if (!connItem.command || typeof connItem.command !== 'object' || typeof connItem.command.command !== 'string') {
    console.error('connection tree item must set a Command object, got: ' + JSON.stringify(connItem.command));
    process.exit(1);
}

ext.deactivate();

const expected = [
    'icg.compile',
    'icg.run',
    'icg.decompile',
    'icg.minify',
    'icg.disasm',
    'icg.graph',
    'icg.tick',
    'icg.tickPanel',
    'icg.restartServer',
    'icg.bench.connect',
    'icg.bench.disconnect',
    'icg.bench.connectionMenu',
    'icg.bench.push',
    'icg.bench.pull',
    'icg.bench.refresh',
    'icg.bench.watch',
    'icg.bench.pause',
    'icg.bench.step',
    'icg.bench.runTicks',
    'icg.bench.reset',
    'icg.bench.ports',
    'icg.bench.writes',
    'icg.bench.compare',
    'icg.bench.exportStack',
    'icg.bench.loadSave',
    'icg.bench.runScenario',
    'icg.bench.openPanel',
    'icg.bench.selectChip',
    'icg.bench.setDevice',
    'icg.bench.pulseDevice',
];
const missing = expected.filter((c) => !registeredCommands.includes(c));
if (missing.length) {
    console.error('missing commands: ' + missing.join(', '));
    process.exit(1);
}
console.log('extension activate/deactivate OK');
