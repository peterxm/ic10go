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

const mock = {
    StatusBarAlignment: { Left: 1, Right: 2 },
    window: {
        createOutputChannel: () => ({ appendLine: noop, append: noop, show: noop, dispose: noop }),
        createStatusBarItem: () => ({ show: noop, hide: noop, dispose: noop }),
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
    commands: { registerCommand: () => disposable(), executeCommand: noop },
    env: { language: 'zh-cn' },
    Uri: { parse: (s) => s },
    Range: class {},
    Position: class {},
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
ext.activate({ subscriptions: [] });
ext.deactivate();
console.log('extension activate/deactivate OK');
