// IC10 Go: in-game testbench integration.
//
// Talks to the `ic10go-testbench` mod (see tools/ingame-testbench) over
// NDJSON/TCP and surfaces it in the editor:
//
//   * a status-bar item showing the connection and selected chip,
//   * an "IC10 Testbench" view (activity bar) with registers, stack and devices,
//   * a beautiful "IC10 Chip State" webview panel,
//   * commands to upload the current .icg, refresh state, toggle live updates,
//     edit a device input and run a testbench scenario.
//
// Written in plain JavaScript with no npm dependencies (Node's built-in `net`),
// matching the rest of the extension.

'use strict';

const vscode = require('vscode');
const net = require('net');
const path = require('path');

function t(en, zh) {
    return (vscode.env.language || 'en').toLowerCase().startsWith('zh') ? zh : en;
}

// ---------------------------------------------------------------------------
// NDJSON client
// ---------------------------------------------------------------------------

class BenchClient {
    constructor(socket) {
        this.socket = socket;
        this.buf = '';
        this.nextId = 1;
        this.pending = new Map();
        this.eventHandlers = [];
        this.closeHandlers = [];
        this.closed = false;
        socket.setEncoding('utf8');
        socket.on('data', (d) => this.onData(d));
        socket.on('error', () => this.shutdown());
        socket.on('close', () => this.shutdown());
        this.socket.setNoDelay(true);
    }

    static connect(host, port, timeoutMs) {
        return new Promise((resolve, reject) => {
            const socket = net.createConnection({ host, port });
            let settled = false;
            const timer = setTimeout(() => {
                if (settled) return;
                settled = true;
                socket.destroy();
                reject(new Error(t('connection timed out', '连接超时')));
            }, timeoutMs || 3000);
            socket.once('error', (err) => {
                if (settled) return;
                settled = true;
                clearTimeout(timer);
                socket.destroy();
                reject(err);
            });
            socket.once('connect', () => {
                if (settled) return;
                settled = true;
                clearTimeout(timer);
                resolve(new BenchClient(socket));
            });
        });
    }

    onData(chunk) {
        this.buf += chunk;
        let i;
        while ((i = this.buf.indexOf('\n')) >= 0) {
            const line = this.buf.slice(0, i);
            this.buf = this.buf.slice(i + 1);
            if (!line.trim()) continue;
            let msg;
            try {
                msg = JSON.parse(line);
            } catch (err) {
                continue;
            }
            this.dispatch(msg);
        }
    }

    dispatch(msg) {
        if (msg.id !== undefined && msg.id !== null && (msg.ok === true || msg.ok === false)) {
            const p = this.pending.get(msg.id);
            if (!p) return;
            this.pending.delete(msg.id);
            if (msg.ok) p.resolve(msg.result || {});
            else {
                const err = new Error((msg.error && msg.error.message) || 'error');
                err.code = msg.error && msg.error.code;
                p.reject(err);
            }
            return;
        }
        if (msg.event) {
            for (const h of this.eventHandlers) {
                try {
                    h(msg);
                } catch (err) {
                    // ignore a broken handler
                }
            }
        }
    }

    call(cmd, args) {
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            if (this.closed) {
                reject(new Error(t('not connected', '未连接')));
                return;
            }
            this.pending.set(id, { resolve, reject });
            this.socket.write(JSON.stringify({ id, cmd, args: args || {} }) + '\n');
            setTimeout(() => {
                if (this.pending.has(id)) {
                    this.pending.delete(id);
                    reject(new Error(cmd + ' ' + t('timed out', '超时')));
                }
            }, 60000);
        });
    }

    onEvent(h) {
        this.eventHandlers.push(h);
    }

    onClose(h) {
        this.closeHandlers.push(h);
    }

    shutdown() {
        if (this.closed) return;
        this.closed = true;
        for (const p of this.pending.values()) p.reject(new Error(t('connection closed', '连接已关闭')));
        this.pending.clear();
        for (const h of this.closeHandlers) {
            try {
                h();
            } catch (err) {
                // ignore
            }
        }
    }

    close() {
        this.closed = true;
        try {
            this.socket.destroy();
        } catch (err) {
            // ignore
        }
    }
}

// ---------------------------------------------------------------------------
// Tree provider
// ---------------------------------------------------------------------------

class BenchTree {
    constructor(bench) {
        this.bench = bench;
        this.emitter = new vscode.EventEmitter();
        this.onDidChangeTreeData = this.emitter.event;
    }

    refresh() {
        this.emitter.fire();
    }

    getTreeItem(el) {
        return el;
    }

    getChildren(el) {
        if (!el) return this.roots();
        switch (el._kind) {
            case 'registers':
                return this.registerItems();
            case 'stack':
                return this.stackItems();
            case 'devices':
                return this.deviceItems();
            case 'chip':
                return this.chipItems();
            case 'chipLeaf':
                return [];
            default:
                return [];
        }
    }

    roots() {
        const b = this.bench;
        const items = [];
        const conn = new vscode.TreeItem(
            b.conn
                ? t(`Connected · ${b.cfg().host}:${b.cfg().port}`, `已连接 · ${b.cfg().host}:${b.cfg().port}`)
                : t('Connect to game…', '连接到游戏…'),
            vscode.TreeItemCollapsibleState.None
        );
        conn.iconPath = new vscode.ThemeIcon(
            b.conn ? 'plug' : 'debug-disconnect',
            b.conn ? new vscode.ThemeColor('testing.iconPassed') : undefined
        );
        conn.command = b.conn ? 'icg.bench.disconnect' : 'icg.bench.connect';
        conn.contextValue = 'connection';
        items.push(conn);

        const chips = b.chips || [];
        const sel = b.state && b.state.chip;
        if (chips.length) {
            for (const chip of chips) {
                items.push(chipNode(chip, sameChip(chip, sel)));
            }
        } else if (sel) {
            items.push(chipNode(sel, true));
        }
        return items;
    }

    chipItems() {
        const st = this.bench.state || {};
        const out = [];
        if (st.registers) out.push(group('registers', t('Registers', '寄存器'), 'symbol-number'));
        if (st.stack) out.push(group('stack', t('Stack', '栈'), 'database'));
        if (st.devices && st.devices.length) out.push(group('devices', t('Devices', '设备'), 'server-process'));
        return out;
    }

    registerItems() {
        const r = (this.bench.state && this.bench.state.registers) || {};
        const order = ['r0', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6', 'r7', 'r8', 'r9', 'r10', 'r11', 'r12', 'r13', 'r14', 'r15', 'ra', 'sp'];
        const out = [];
        for (const k of order) {
            if (!(k in r)) continue;
            const item = new vscode.TreeItem(k, vscode.TreeItemCollapsibleState.None);
            item.description = String(r[k]);
            item.iconPath = new vscode.ThemeIcon(k === 'sp' ? 'arrow-both' : k === 'ra' ? 'reply' : 'symbol-number');
            if (k === 'sp' || k === 'ra') item.iconPath = new vscode.ThemeIcon('symbol-constant', new vscode.ThemeColor('charts.orange'));
            out.push(item);
        }
        return out;
    }

    stackItems() {
        const s = (this.bench.state && this.bench.state.stack) || { sp: 0, values: {} };
        const vals = s.values || {};
        const sp = s.sp || 0;
        const keys = Object.keys(vals).map(Number).sort((a, b) => a - b);
        const out = [];
        for (const i of keys) {
            if (i > sp && vals[String(i)] === 0) continue; // hide empty slots above sp
            const item = new vscode.TreeItem(`[${i}]`, vscode.TreeItemCollapsibleState.None);
            item.description = String(vals[String(i)]);
            if (i === sp) {
                item.iconPath = new vscode.ThemeIcon('arrow-right', new vscode.ThemeColor('charts.orange'));
                item.tooltip = t('stack pointer', '栈指针');
            } else {
                item.iconPath = new vscode.ThemeIcon('blank');
            }
            out.push(item);
            if (out.length >= 128) {
                const more = new vscode.TreeItem(t('… more in the panel', '…更多见面板'), vscode.TreeItemCollapsibleState.None);
                more.iconPath = new vscode.ThemeIcon('ellipsis');
                out.push(more);
                break;
            }
        }
        return out;
    }

    deviceItems() {
        const devices = (this.bench.state && this.bench.state.devices) || [];
        const out = [];
        for (const d of devices) {
            const present = d.present !== false;
            const item = new vscode.TreeItem(
                d.port,
                present ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.None
            );
            item._kind = present ? 'device' : 'deviceEmpty';
            item.description = [d.binding, present ? d.name || d.prefab : t('empty', '空')]
                .filter(Boolean)
                .join(' · ');
            item.iconPath = new vscode.ThemeIcon(
                'server-process',
                present ? undefined : new vscode.ThemeColor('disabledForeground')
            );
            item._logic = present ? d.logic || {} : {};
            out.push(item);
        }
        return out;
    }
}

function group(kind, label, icon) {
    const item = new vscode.TreeItem(label, vscode.TreeItemCollapsibleState.Collapsed);
    item._kind = kind;
    item.iconPath = new vscode.ThemeIcon(icon);
    return item;
}

function sameChip(a, b) {
    if (!a || !b) return false;
    if (a.id && b.id) return a.id === b.id;
    return (a.name || a.prefab) === (b.name || b.prefab);
}

// chipNode renders one host/chip in the tree. The selected chip expands into
// Registers / Stack / Devices; others are clickable to select.
function chipNode(chip, selected) {
    const name = chip.name || chip.prefab || `chip#${chip.index}`;
    const item = new vscode.TreeItem(
        name,
        selected ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.None
    );
    item._kind = selected ? 'chip' : 'chipLeaf';
    item._chip = chip;
    item.iconPath = new vscode.ThemeIcon(
        'circuit-board',
        selected ? new vscode.ThemeColor('charts.blue') : undefined
    );
    const tags = [];
    if (chip.programmable === false) tags.push(t('no chip', '无芯片'));
    if (chip.lines) tags.push(`${chip.lines} ${t('lines', '行')}`);
    item.description = tags.join(' · ');
    let tip = `**${name}**\n\n`;
    if (chip.prefab) tip += `\`${chip.prefab}\`\n\n`;
    if (chip.chipPrefab) tip += t('chip: ', '芯片：') + `\`${chip.chipPrefab}\`\n\n`;
    item.tooltip = new vscode.MarkdownString(tip);
    if (!selected && chip.programmable !== false) {
        item.command = {
            command: 'icg.bench.selectChip',
            title: t('Select', '选择'),
            arguments: [chip],
        };
    }
    return item;
}

// getChildren for a device node needs its logic map; BenchTree.getChildren
// handles the standard kinds above, so extend it here for 'device'.
const baseGetChildren = BenchTree.prototype.getChildren;
BenchTree.prototype.getChildren = function (el) {
    if (el && el._kind === 'device') {
        const out = [];
        for (const k of Object.keys(el._logic || {})) {
            const item = new vscode.TreeItem(k, vscode.TreeItemCollapsibleState.None);
            item.description = String(el._logic[k]);
            item.iconPath = new vscode.ThemeIcon('symbol-field');
            item.contextValue = 'deviceLogic';
            item.command = {
                command: 'icg.bench.setDevice',
                title: 'Set value',
                arguments: [{ port: el.label, logic: k, value: el._logic[k] }],
            };
            out.push(item);
        }
        return out;
    }
    return baseGetChildren.call(this, el);
};

// ---------------------------------------------------------------------------
// Main integration
// ---------------------------------------------------------------------------

class Bench {
    constructor(client) {
        this.client = client; // LspClient: execCli / buildFlags / withTempFile / output / activeICG
        this.conn = undefined;
        this.state = undefined;
        this.chips = [];
        this.watching = false;
        this.tree = undefined;
        this.panel = undefined;
        this.report = undefined;
        this.status = undefined;
    }

    cfg() {
        const c = vscode.workspace.getConfiguration('icg');
        return {
            host: c.get('bench.host') || '127.0.0.1',
            port: c.get('bench.port') || 7800,
            autoConnect: c.get('bench.autoConnect') !== false,
            refreshInterval: c.get('bench.refreshInterval') || 0,
            watchOnOpen: c.get('bench.watchOnOpen') === true,
        };
    }

    register(context) {
        this.status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 90);
        this.status.command = 'icg.bench.openPanel';
        context.subscriptions.push(this.status);
        this.setStatus(false);

        this.tree = new BenchTree(this);
        context.subscriptions.push(
            vscode.window.registerTreeDataProvider('icg.bench', this.tree)
        );

        const cmd = (name, fn) => context.subscriptions.push(vscode.commands.registerCommand(name, fn));
        cmd('icg.bench.connect', () => this.connect(false));
        cmd('icg.bench.disconnect', () => this.disconnect());
        cmd('icg.bench.push', () => this.push());
        cmd('icg.bench.refresh', () => this.refresh(true));
        cmd('icg.bench.watch', () => this.toggleWatch());
        cmd('icg.bench.pause', () => this.togglePause());
        cmd('icg.bench.loadSave', () => this.loadSave());
        cmd('icg.bench.runScenario', () => this.runScenario());
        cmd('icg.bench.openPanel', () => this.openPanel());
        cmd('icg.bench.selectChip', (chip) => this.selectChip(chip));
        cmd('icg.bench.setDevice', (arg) => this.setDevice(arg));

        const it = this.cfg().refreshInterval;
        if (it > 0) {
            const timer = setInterval(() => this.refresh(false), it);
            context.subscriptions.push({ dispose: () => clearInterval(timer) });
        }

        context.subscriptions.push(
            vscode.workspace.onDidChangeConfiguration((e) => {
                if (e.affectsConfiguration && e.affectsConfiguration('icg.bench')) this.setStatus(!!this.conn);
            })
        );

        if (this.cfg().autoConnect) {
            this.connect(true).catch(() => {});
        }
    }

    dispose() {
        this.disconnect();
        if (this.panel) this.panel.dispose();
        if (this.report) this.report.dispose();
    }

    // -- connection -------------------------------------------------------

    async connect(quiet) {
        if (this.conn) {
            if (!quiet) await this.refresh(true);
            return this.conn;
        }
        const { host, port } = this.cfg();
        try {
            this.conn = await BenchClient.connect(host, port);
            this.conn.onEvent((ev) => this.onEvent(ev));
            this.conn.onClose(() => this.onClose());
            this.setStatus(true);
            if (!quiet) {
                vscode.window.setStatusBarMessage(
                    t(`IC10: connected to the game (${host}:${port})`, `IC10: 已连接游戏（${host}:${port}）`),
                    3000
                );
            }
            await this.refresh(false);
            return this.conn;
        } catch (err) {
            this.conn = undefined;
            this.setStatus(false);
            if (!quiet) {
                const retry = t('Retry', '重试');
                const settings = t('Settings', '设置');
                const pick = await vscode.window.showErrorMessage(
                    t(
                        `IC10: could not connect to the game testbench at ${host}:${port}. Is Stationeers running with the ic10go-testbench mod enabled?`,
                        `IC10: 无法连接游戏测试台 ${host}:${port}。游戏是否在运行且启用了 ic10go-testbench mod？`
                    ),
                    retry,
                    settings
                );
                if (pick === retry) return this.connect(false);
                if (pick === settings) {
                    vscode.commands.executeCommand('workbench.action.openSettings', 'icg.bench');
                }
            }
            return undefined;
        }
    }

    disconnect() {
        if (this.conn) {
            this.conn.close();
            this.conn = undefined;
        }
        this.watching = false;
        this.state = undefined;
        this.setStatus(false);
        if (this.tree) this.tree.refresh();
        this.renderPanel();
    }

    onClose() {
        this.conn = undefined;
        this.watching = false;
        this.setStatus(false);
        if (this.tree) this.tree.refresh();
        this.renderPanel();
    }

    setStatus(connected) {
        if (!this.status) return;
        const chip = this.state && this.state.chip;
        const name = chip ? chip.name || chip.prefab || `chip#${chip.index}` : '';
        this.status.text = connected ? `$(circuit-board) IC10: ${name || t('game', '游戏')}` : '$(circuit-board) IC10';
        this.status.tooltip = connected
            ? t('IC10 testbench connected — click to open the panel', 'IC10 测试台已连接——点击打开面板')
            : t('IC10 testbench disconnected — click to open the panel', 'IC10 测试台未连接——点击打开面板');
        this.status.color = connected ? undefined : new vscode.ThemeColor('disabledForeground');
        this.status.show();
    }

    // -- commands ---------------------------------------------------------

    async refresh(interactive) {
        const c = await this.connect(true);
        if (!c) {
            if (interactive) vscode.window.showWarningMessage(t('IC10: not connected to the game.', 'IC10: 未连接到游戏。'));
            return;
        }
        try {
            const [st, list] = await Promise.all([
                c.call('state', { include: ['registers', 'stack', 'devices', 'program', 'errors'], all: true }),
                c.call('chip.list', {}).catch(() => ({ chips: [] })),
            ]);
            this.state = st;
            this.chips = list.chips || [];
            if (this.tree) this.tree.refresh();
            this.renderPanel();
            this.setStatus(true);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench refresh failed: ${err.message}`);
            if (interactive) vscode.window.showErrorMessage(t('IC10: refresh failed. See the "IC10 Go" output.', 'IC10: 刷新失败，详见 "IC10 Go" 输出面板。'));
        }
    }

    async selectChip(chip) {
        if (!chip) return;
        const c = await this.connect(false);
        if (!c) return;
        try {
            await c.call('chip.select', { chip: { index: chip.index } });
            await this.refresh(false);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: select chip failed: ', 'IC10: 选择芯片失败：') + err.message);
        }
    }

    async push() {
        const doc = this.client.activeICG();
        if (!doc) return;
        const conn = await this.connect(false);
        if (!conn) return;
        await this.client.withTempFile(doc, async (tmp) => {
            const args = ['build', '--json', ...this.client.buildFlags(this.client.config()), tmp];
            const res = await this.client.execCli(args);
            let out;
            try {
                out = JSON.parse(res.stdout);
            } catch (err) {
                this.client.output.appendLine(`=== push: compile failed ===\n${res.stderr || res.stdout}`);
                this.client.output.show(true);
                vscode.window.showErrorMessage(t('IC10: compile failed. See the "IC10 Go" output.', 'IC10: 编译失败，详见 "IC10 Go" 输出面板。'));
                return;
            }
            if (!out.ok) {
                this.client.output.appendLine(`=== push: ${path.basename(doc.fileName)} has errors ===`);
                for (const d of out.diagnostics || []) {
                    const at = d.range && d.range.start ? `${d.range.start.line}:${d.range.start.col}` : '?';
                    this.client.output.appendLine(`${at} ${d.severity}: ${d.message}${d.code ? ' [' + d.code + ']' : ''}`);
                }
                this.client.output.show(true);
                vscode.window.showErrorMessage(t('IC10: cannot upload, the program has errors.', 'IC10: 程序有错误，无法上传。'));
                return;
            }
            const loaders = (out.data && (out.data.loaders || (out.data.loader ? [out.data.loader] : []))) || [];
            try {
                const r = await conn.call('push', { code: out.code, loaders });
                const lines = r.lines || (out.stats && out.stats.lines) || 0;
                vscode.window.setStatusBarMessage(
                    t(`IC10: uploaded ${lines} lines${loaders.length ? ` (+${loaders.length} loader)` : ''}`,
                        `IC10: 已上传 ${lines} 行${loaders.length ? `（+${loaders.length} 段 loader）` : ''}`),
                    4000
                );
                await this.refresh(false);
            } catch (err) {
                this.client.output.appendLine(`IC10 bench push failed: ${err.message}`);
                vscode.window.showErrorMessage(t('IC10: upload failed. See the "IC10 Go" output.', 'IC10: 上传失败，详见 "IC10 Go" 输出面板。'));
            }
        });
    }

    async togglePause() {
        const c = await this.connect(false);
        if (!c) return;
        const want = !(this.state && this.state.paused);
        try {
            await c.call('pause', { on: want });
            await this.refresh(false);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: pause failed: ', 'IC10: 暂停失败：') + err.message);
        }
    }

    async loadSave() {
        const c = await this.connect(false);
        if (!c) return;
        let saves = [];
        try {
            const r = await c.call('world.saves', {});
            saves = r.saves || [];
        } catch (err) {
            this.client.output.appendLine(`IC10 bench saves failed: ${err.message}`);
        }
        if (!saves.length) {
            vscode.window.showWarningMessage(t('IC10: no saves found.', 'IC10: 未找到存档。'));
            return;
        }
        const pick = await vscode.window.showQuickPick(saves, {
            title: t('Load a save into the game', '在游戏中载入存档'),
            placeHolder: t('save folder', '存档名'),
        });
        if (!pick) return;
        try {
            const r = await c.call('world.load', { save: pick });
            this.client.output.appendLine(`IC10 load "${pick}": ${r.result || ''}`);
            vscode.window.setStatusBarMessage(t('IC10: loading ', 'IC10: 正在载入 ') + pick, 6000);
            setTimeout(() => this.refresh(false), 8000);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: load failed: ', 'IC10: 载入失败：') + err.message);
        }
    }

    async toggleWatch() {
        const c = await this.connect(false);
        if (!c) return;
        this.watching = !this.watching;
        const interval = this.cfg().refreshInterval > 0 ? this.cfg().refreshInterval : 250;
        try {
            await c.call('watch', { on: this.watching, intervalMs: interval, all: true });
            vscode.window.setStatusBarMessage(
                this.watching ? t('IC10: live updates on', 'IC10: 实时更新已开启') : t('IC10: live updates off', 'IC10: 实时更新已关闭'),
                2500
            );
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: watch failed: ', 'IC10: 开启实时更新失败：') + err.message);
        }
    }

    onEvent(ev) {
        if (ev.event === 'state' && ev.state) {
            this.state = ev.state;
            if (this.tree) this.tree.refresh();
            this.renderPanel();
            this.setStatus(true);
        }
    }

    async setDevice(arg) {
        if (!arg || !arg.port || !arg.logic) return;
        const input = await vscode.window.showInputBox({
            title: t(`Set ${arg.port}.${arg.logic}`, `设置 ${arg.port}.${arg.logic}`),
            value: String(arg.value),
            prompt: t('New value', '新的值'),
        });
        if (input === undefined) return;
        const value = Number(input);
        if (!isFinite(value)) {
            vscode.window.showErrorMessage(t('IC10: not a number', 'IC10: 不是合法数字'));
            return;
        }
        const c = await this.connect(false);
        if (!c) return;
        try {
            await c.call('set', { writes: [{ port: arg.port, logic: arg.logic, value }] });
            await this.refresh(false);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: set failed: ', 'IC10: 设置失败：') + err.message);
        }
    }

    async runScenario() {
        const pick = await vscode.window.showOpenDialog({
            canSelectMany: false,
            openLabel: t('Run testbench scenario', '运行测试台场景'),
            filters: { 'IC10 testbench': ['json'] },
        });
        if (!pick || !pick.length) return;
        const file = pick[0].fsPath;
        const res = await this.client.execCli(['testbench', 'run', '--json', file]);
        let out;
        try {
            out = JSON.parse(res.stdout);
        } catch (err) {
            this.client.output.appendLine(`=== testbench run failed ===\n${res.stderr || res.stdout}`);
            this.client.output.show(true);
            vscode.window.showErrorMessage(t('IC10: testbench run failed. See the "IC10 Go" output.', 'IC10: 测试台运行失败，详见 "IC10 Go" 输出面板。'));
            return;
        }
        this.showReport(file, out);
    }

    // -- webview panel ----------------------------------------------------

    openPanel() {
        if (this.panel) {
            this.panel.reveal(vscode.ViewColumn.Beside);
            this.renderPanel();
        } else {
            this.panel = vscode.window.createWebviewPanel('icg.benchState', t('IC10 Chip State', 'IC10 芯片状态'), vscode.ViewColumn.Beside, {
                enableScripts: true,
                retainContextWhenHidden: true,
            });
            this.panel.webview.html = this.panelHtml();
            this.panel.onDidDispose(() => {
                this.panel = undefined;
            });
            this.panel.webview.onDidReceiveMessage((m) => this.onPanelMessage(m));
        }
        if (this.cfg().watchOnOpen && !this.watching) this.toggleWatch();
        this.renderPanel();
    }

    onPanelMessage(m) {
        if (!m) return;
        if (m.type === 'refresh') this.refresh(true);
        else if (m.type === 'watch') this.toggleWatch();
        else if (m.type === 'pause') this.togglePause();
    }

    renderPanel() {
        if (!this.panel) return;
        this.panel.webview.postMessage({
            type: 'state',
            state: this.state,
            connected: !!this.conn,
            watching: this.watching,
        });
    }

    panelHtml() {
        const nonce = String(Date.now()) + Math.random().toString(36).slice(2);
        const csp = `default-src 'none'; style-src ${this.panel.webview.cspSource} 'unsafe-inline'; script-src 'nonce-${nonce}';`;
        return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
  :root { color-scheme: light dark; }
  body {
    font-family: var(--vscode-font-family);
    font-size: var(--vscode-font-size);
    color: var(--vscode-foreground);
    background: transparent;
    padding: 0; margin: 0;
  }
  header {
    position: sticky; top: 0; z-index: 2;
    display: flex; align-items: center; gap: 10px;
    padding: 10px 16px;
    background: var(--vscode-sideBar-background, var(--vscode-editor-background));
    border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35));
  }
  header .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--vscode-disabledForeground); }
  header .dot.on { background: var(--vscode-testing-iconPassed, #3fb950); box-shadow: 0 0 6px var(--vscode-testing-iconPassed, #3fb950); }
  header .title { font-weight: 600; }
  header .line { color: var(--vscode-descriptionForeground); margin-left: auto; font-variant-numeric: tabular-nums; }
  button {
    font-family: inherit; font-size: inherit;
    color: var(--vscode-button-secondaryForeground, var(--vscode-foreground));
    background: var(--vscode-button-secondaryBackground, transparent);
    border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35));
    border-radius: 5px; padding: 3px 10px; cursor: pointer;
  }
  button:hover { background: var(--vscode-button-secondaryHoverBackground, rgba(128,128,128,.15)); }
  button.active { color: var(--vscode-button-foreground); background: var(--vscode-button-background); border-color: var(--vscode-button-background); }
  main { padding: 14px 16px 32px; }
  section { margin-bottom: 18px; }
  h2 {
    font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: .08em;
    color: var(--vscode-descriptionForeground); margin: 0 0 8px;
  }
  summary {
    cursor: pointer; font-size: 11px; font-weight: 600; text-transform: uppercase;
    letter-spacing: .08em; color: var(--vscode-descriptionForeground); margin: 0 0 8px;
  }
  summary::marker { color: var(--vscode-descriptionForeground); }
  summary .pill { text-transform: none; letter-spacing: 0; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(108px, 1fr)); gap: 6px; }
  .cell {
    display: flex; justify-content: space-between; gap: 8px;
    padding: 5px 9px; border-radius: 5px;
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.08));
    border: 1px solid transparent;
  }
  .cell .k { color: var(--vscode-descriptionForeground); }
  .cell .v { font-family: var(--vscode-editor-font-family, monospace); font-variant-numeric: tabular-nums; }
  .cell.special { border-color: var(--vscode-charts-orange, #d18616); }
  .cell.changed { animation: flash 1.1s ease-out; }
  @keyframes flash { 0% { background: var(--vscode-testing-iconPassed, #3fb950); color: #000; } 100% {} }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 4px 8px; border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.25)); }
  th { color: var(--vscode-descriptionForeground); font-weight: 500; }
  td.port { font-family: var(--vscode-editor-font-family, monospace); color: var(--vscode-charts-blue, #3794ff); }
  td.num { font-family: var(--vscode-editor-font-family, monospace); text-align: right; font-variant-numeric: tabular-nums; }
  td input {
    width: 90px; text-align: right; font-family: var(--vscode-editor-font-family, monospace);
    color: var(--vscode-input-foreground); background: var(--vscode-input-background);
    border: 1px solid var(--vscode-input-border, rgba(128,128,128,.35)); border-radius: 4px; padding: 1px 5px;
  }
  .empty { color: var(--vscode-descriptionForeground); font-style: italic; }
  .muted { color: var(--vscode-descriptionForeground); }
  details.dev {
    margin: 0 0 4px; padding: 3px 9px; border-radius: 6px;
    border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.25));
  }
  details.dev > summary {
    text-transform: none; letter-spacing: 0; font-size: var(--vscode-font-size);
    font-weight: 500; margin: 0;
  }
  details.dev table { margin-top: 4px; }
  details.dev td { padding: 2px 8px; border-bottom: none; }
  .dev.empty {
    display: flex; align-items: center; gap: 8px; margin-bottom: 4px; padding: 3px 9px;
    border: 1px dashed var(--vscode-editorWidget-border, rgba(128,128,128,.25));
    border-radius: 6px; color: var(--vscode-descriptionForeground);
  }
  .port { font-family: var(--vscode-editor-font-family, monospace); color: var(--vscode-charts-blue, #3794ff); }
  .pill { font-size: 10px; padding: 1px 7px; border-radius: 999px; background: var(--vscode-badge-background); color: var(--vscode-badge-foreground); }
</style>
</head>
<body>
<header>
  <span class="dot" id="dot"></span>
  <span class="title" id="chip">IC10</span>
  <span class="pill" id="prog" style="display:none"></span>
  <span style="flex:1"></span>
  <span class="line" id="line"></span>
  <button id="pause">Pause</button>
  <button id="watch">Watch</button>
  <button id="refresh">Refresh</button>
</header>
<main id="main"><p class="empty">Not connected.</p></main>
<script nonce="${nonce}">
  const vscode = acquireVsCodeApi();
  let prev = {};
  document.getElementById('refresh').addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));
  document.getElementById('watch').addEventListener('click', () => vscode.postMessage({ type: 'watch' }));
  document.getElementById('pause').addEventListener('click', () => vscode.postMessage({ type: 'pause' }));
  window.addEventListener('message', (e) => {
    const m = e.data;
    if (!m || m.type !== 'state') return;
    render(m);
  });
  function num(v) {
    if (v === null || v === undefined) return '–';
    if (Number.isInteger(v)) return String(v);
    return String(Number(v.toPrecision(10)));
  }
  function cell(k, v, special) {
    const changed = prev[k] !== undefined && prev[k] !== v;
    return '<div class="cell' + (special ? ' special' : '') + (changed ? ' changed' : '') + '">' +
      '<span class="k">' + k + '</span><span class="v">' + num(v) + '</span></div>';
  }
  function render(m) {
    const st = m.state;
    document.getElementById('dot').className = 'dot' + (m.connected ? ' on' : '');
    document.getElementById('watch').className = m.watching ? 'active' : '';
    const pauseBtn = document.getElementById('pause');
    if (pauseBtn) {
      const paused = !!(st && st.paused);
      pauseBtn.textContent = paused ? 'Resume' : 'Pause';
      pauseBtn.className = paused ? 'active' : '';
    }
    const main = document.getElementById('main');
    if (!st || !st.chip) {
      document.getElementById('chip').textContent = 'IC10';
      document.getElementById('line').textContent = '';
      document.getElementById('prog').style.display = 'none';
      main.innerHTML = '<p class="empty">' + (m.connected ? 'No chip. Load the test-bench save.' : 'Not connected.') + '</p>';
      return;
    }
    const chip = st.chip;
    document.getElementById('chip').textContent = chip.name || chip.prefab || ('chip#' + chip.index);
    const prog = document.getElementById('prog');
    if (st.program) { prog.style.display = ''; prog.textContent = st.program.lines + ' lines'; }
    else prog.style.display = 'none';
    document.getElementById('line').textContent = st.line !== undefined ? 'line ' + st.line : '';

    const next = {};
    let html = '';
    if (st.registers) {
      html += '<section><h2>Registers</h2><div class="grid">';
      const order = ['r0','r1','r2','r3','r4','r5','r6','r7','r8','r9','r10','r11','r12','r13','r14','r15','ra','sp'];
      for (const k of order) if (k in st.registers) { next[k] = st.registers[k]; html += cell(k, st.registers[k], k === 'sp' || k === 'ra'); }
      html += '</div></section>';
    }
    if (st.stack) {
      const sp = st.stack.sp || 0;
      const vals = st.stack.values || {};
      const keys = Object.keys(vals).map(Number).sort((a, b) => a - b);
      html += '<section><details><summary>Stack <span class="pill">sp ' + sp + ' / ' + (st.stack.size || keys.length || '?') + ' · ' + keys.length + ' slots</span></summary><div class="grid">';
      for (const i of keys) {
        const v = vals[String(i)];
        next['[' + i + ']'] = v;
        html += cell('[' + i + ']', v, i === sp);
      }
      html += '</div></details></section>';
    }
    if (st.devices && st.devices.length) {
      html += '<section><h2>Devices</h2>';
      for (const d of st.devices) {
        const keys = Object.keys(d.logic || {}).sort();
        const binding = d.binding ? '<span class="k">' + d.binding + '</span>' : '';
        const present = d.present !== false;
        if (!present || !keys.length) {
          html += '<div class="dev empty"><span class="port">' + d.port + '</span> ' + binding +
            ' <span class="muted">empty</span></div>';
          continue;
        }
        const desc = d.name || d.prefab || '';
        html += '<details class="dev"><summary><span class="port">' + d.port + '</span> ' + binding +
          ' <span class="muted">' + desc + '</span> <span class="pill">' + keys.length + ' logic</span></summary><table>';
        for (const k of keys) {
          next[d.port + '.' + k] = d.logic[k];
          html += '<tr><td>' + k + '</td><td class="num">' + num(d.logic[k]) + '</td></tr>';
        }
        html += '</table></details>';
      }
      html += '</section>';
    }
    if (st.errors && (st.errors.code || st.errors.compilation)) {
      html = '<section><h2>Error</h2><p>' + (st.errors.code || '') + ' line ' + st.errors.line + '</p></section>' + html;
    }
    prev = next;
    main.innerHTML = html;
  }
  vscode.postMessage({ type: 'refresh' });
</script>
</body>
</html>`;
    }

    showReport(file, report) {
        const nonce = String(Date.now()) + Math.random().toString(36).slice(2);
        const rows = (report.cases || [])
            .map((c) => {
                const ok = c.passed;
                const detail = c.error
                    ? escapeHtml(c.error)
                    : (c.failures || [])
                          .map((f) => `${escapeHtml(f.key)}: want ${escapeHtml(String(f.want))}, got ${escapeHtml(String(f.got))}`)
                          .join('<br>');
                return `<tr class="${ok ? 'ok' : 'bad'}"><td>${ok ? '✓' : '✗'}</td><td>${escapeHtml(c.name || '')}</td><td>${detail}</td></tr>`;
            })
            .join('');
        const summary = `${report.passed || 0} passed, ${report.failed || 0} failed`;
        const html = `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline';">
<style>
  body { font-family: var(--vscode-font-family); color: var(--vscode-foreground); padding: 16px; }
  h1 { font-size: 15px; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: 5px 9px; border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.3)); vertical-align: top; }
  th { color: var(--vscode-descriptionForeground); font-weight: 500; }
  tr.ok td:first-child { color: var(--vscode-testing-iconPassed, #3fb950); }
  tr.bad td:first-child { color: var(--vscode-testing-iconFailed, #f14c4c); }
  code { font-family: var(--vscode-editor-font-family, monospace); }
</style></head>
<body>
<h1>${escapeHtml(path.basename(file))} — ${escapeHtml(summary)}</h1>
<table><thead><tr><th></th><th>case</th><th>detail</th></tr></thead><tbody>${rows}</tbody></table>
</body></html>`;
        if (this.report) {
            this.report.dispose();
        }
        this.report = vscode.window.createWebviewPanel('icg.benchReport', t('IC10 Testbench Report', 'IC10 测试台报告'), vscode.ViewColumn.Beside, {});
        this.report.webview.html = html;
        this.report.onDidDispose(() => {
            this.report = undefined;
        });
    }
}

function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

module.exports = { Bench };
