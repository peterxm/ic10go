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
        registerSignatureHelpProvider: disposable,
        registerCodeActionsProvider: disposable,
        registerInlayHintsProvider: disposable,
        registerDocumentSemanticTokensProvider: disposable,
    },
    workspace: {
        onDidOpenTextDocument: disposable,
        onDidChangeTextDocument: disposable,
        onDidCloseTextDocument: disposable,
        getConfiguration: () => ({ get: () => undefined }),
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
    SemanticTokens: class {},
    SemanticTokensLegend: class {},
    Diagnostic: class {},
    DiagnosticSeverity: { Error: 1, Warning: 2, Information: 3 },
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
