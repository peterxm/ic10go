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
    OverviewRulerLane: { Left: 1, Center: 2, Right: 3, Full: 4 },
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
    Uri: { parse: (s) => s, file: (s) => s },
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
    TextEditorRevealType: { InCenter: 1 },
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
const ctx = { subscriptions: [], workspaceState: { get: () => undefined, update: () => Promise.resolve() }, asAbsolutePath: (p) => p };
ext.activate(ctx);

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
// panelHtml evaluates every template interpolation, so generating it catches a
// broken ${...} even though the watch/register rows are built later in the
// webview. The poke plumbing ("run to cursor"/"change value") must be present.
bench.panel = { webview: { cspSource: 'vscode-webview://test' } };
const panel = bench.panelHtml();
bench.panel = undefined;

// The chip-state panel's search box must find a device by ReferenceId and order
// the network list by name. Run the helpers straight out of the generated script
// (they live in the webview template, so nothing else exercises them).
function panelFn(src, name) {
    const start = src.indexOf('function ' + name + '(');
    if (start < 0) throw new Error('panel script is missing ' + name);
    let depth = 0;
    for (let i = src.indexOf('{', start); i < src.length; i++) {
        if (src[i] === '{') depth++;
        else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
    }
    throw new Error('unterminated ' + name);
}
const helpers = new Function(
    panelFn(panel, 'devMatches') + '\n' + panelFn(panel, 'devSortKey') +
    '\nreturn { devMatches: devMatches, devSortKey: devSortKey };'
)();
const devs = [
    { id: 1725919, name: '', prefab: 'StructureDiode' },
    { id: 1550107, name: 'Interior Door 01 - Main Airlock 01', prefab: 'StructureAirlockWide' },
    { id: 1554176, name: 'Gas Sensor 01 - Main Airlock 01', prefab: 'StructureGasSensor' },
];
if (!helpers.devMatches(devs[1], '1550107')) {
    console.error('the panel filter must match a ReferenceId');
    process.exit(1);
}
if (helpers.devMatches(devs[0], 'interior')) {
    console.error('the panel filter matched an unrelated device');
    process.exit(1);
}
if (!helpers.devMatches(devs[2], 'gassensor')) {
    console.error('the panel filter must match the prefab');
    process.exit(1);
}
const ordered = devs
    .slice()
    .sort((a, b) => {
        const ka = helpers.devSortKey(a);
        const kb = helpers.devSortKey(b);
        return ka < kb ? -1 : ka > kb ? 1 : 0;
    })
    .map((d) => d.id)
    .join(',');
if (ordered !== '1554176,1550107,1725919') {
    console.error('network devices should sort by name then id, got ' + ordered);
    process.exit(1);
}

// Every device row in the panel offers a track button -- but only when the mod
// gave the entry a ReferenceId (an older mod's port devices have none).
const trackBtn = new Function(
    'const TRACK_HINT = "hint"; const TRACK_ICON = "T";\n' +
    panelFn(panel, 'esc') + '\n' + panelFn(panel, 'trackBtn') +
    '\nreturn trackBtn;'
)();
const btn = trackBtn(1550107, 'Interior Door 01');
if (!btn.includes('class="trackdev"') || !btn.includes('data-id="1550107"') || !btn.includes('data-label="Interior Door 01"')) {
    console.error('the panel track button is missing its target: ' + btn);
    process.exit(1);
}
if (trackBtn(undefined, 'x') !== '') {
    console.error('an id-less device must not get a track button');
    process.exit(1);
}
if (!panel.includes('acquireVsCodeApi') || !panel.includes('POKE_HINT') || !panel.includes('setValue')) {
    console.error('panelHtml is missing the poke plumbing');
    process.exit(1);
}
// runtimeLine is runLine's inverse: the first IC10 line for the source line,
// else the first one after it (the map is IC10 line -> source line, 1-based).
bench.programMap = { uri: 'file://t.icg', map: [0, 1, 2, 2, 3] };
const doc = { uri: { toString: () => 'file://t.icg' }, languageId: 'icg', lineCount: 10 };
if (bench.runtimeLine(doc, 2) !== 1 || bench.runtimeLine(doc, 3) !== 3 || bench.runtimeLine(doc, 9) !== null) {
    console.error('runtimeLine mapping wrong: ' + [bench.runtimeLine(doc, 2), bench.runtimeLine(doc, 3), bench.runtimeLine(doc, 9)]);
    process.exit(1);
}
bench.programMap = undefined;

const chunks = bench.buildStackLoader([[0, 1], [5, 2.5], [509, 79464601]], 'get', 2);if (chunks.length !== 2 || !chunks[0].startsWith('put db 0 1') || !chunks[1].includes('put db 509 79464601')) {
    console.error('buildStackLoader chunking/format wrong: ' + JSON.stringify(chunks));
    process.exit(1);
}
if (bench.buildStackLoader([[3, 0]], 'stack', 0)[0].trim() !== 'poke 3 0') {
    console.error('buildStackLoader did not use poke for stack access');
    process.exit(1);
}

// The connected tree node must use a Command object: a bare string made VSCode
// read command "undefined" and fail with "command 'undefined' not found".
bench.register(ctx);
bench.conn = {}; // pretend connected
bench.chips = [];
const roots = bench.tree.getChildren(undefined);
const connItem = roots[0];
if (!connItem.command || typeof connItem.command !== 'object' || typeof connItem.command.command !== 'string') {
    console.error('connection tree item must set a Command object, got: ' + JSON.stringify(connItem.command));
    process.exit(1);
}

// scoreNote renders the per-tick numbers (and the grade) for the push message.
// locale-independent: the mock runs zh-cn, and both wordings contain these.
const sn = bench.scoreNote({ tick: { cost: 29, limit: 128, ticksPerRound: 1 }, score: { grade: 'A' } });
if (!sn.includes('29/128') || !sn.includes('A') || /round|每轮/.test(sn)) {
    console.error('scoreNote wrong for a one-tick program: ' + sn);
    process.exit(1);
}
const sn2 = bench.scoreNote({ tick: { cost: 129, limit: 128, ticksPerRound: 2 }, score: { grade: 'D' } });
if (!sn2.includes('129/128') || !sn2.includes('2 tick') || !sn2.includes('D')) {
    console.error('scoreNote wrong for an over-budget program: ' + sn2);
    process.exit(1);
}
if (bench.scoreNote(undefined) !== '' || bench.scoreNote({}) !== '') {
    console.error('scoreNote should be empty without a build report');
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
    'icg.bench.runToLine',
    'icg.bench.setValue',
];
const missing = expected.filter((c) => !registeredCommands.includes(c));
if (missing.length) {
    console.error('missing commands: ' + missing.join(', '));
    process.exit(1);
}
console.log('extension activate/deactivate OK');
