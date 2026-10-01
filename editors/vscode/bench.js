// IC10 Go: in-game testbench integration.
//
// Talks to the `ic10go-testbench` mod (see tools/ingame-testbench) over
// NDJSON/TCP and surfaces it in the editor:
//
//   * a status-bar item showing the connection and selected chip,
//   * an "IC10 Testbench" view (activity bar) with registers, stack and devices,
//   * a beautiful "IC10 Chip State" webview panel,
//   * commands to upload/download the current .icg, refresh state, toggle live
//     updates, edit a device input and run a testbench scenario.
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

// HASH_LOGIC names the device logic entries whose numeric value is a prefab
// hash (see the game's LogicType table), so they can be labelled with the
// prefab they refer to.
const HASH_LOGIC = /^(PrefabHash|NameHash|OccupantHash)$/;

// fmtStackValue renders a stack value as an IC10 numeric literal (integers with
// no decimal point), matching what a data loader writes.
function fmtStackValue(v) {
    const n = Number(v);
    if (!isFinite(n)) return '0';
    if (Number.isInteger(n)) return String(n);
    return String(Number(n.toPrecision(10)));
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
        try {
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
                case 'chipGroup':
                    return this.chipGroupItems(el);
                case 'chipLeaf':
                    return [];
                default:
                    return [];
            }
        } catch (err) {
            const out = this.bench && this.bench.client && this.bench.client.output;
            if (out) out.appendLine('IC10 tree: ' + (err && err.message));
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
        // TreeItem.command must be a Command object, not a bare string: VSCode
        // reads .command off it, so a string yields "command 'undefined'".
        conn.command = b.conn
            ? { command: 'icg.bench.connectionMenu', title: t('Reconnect', '重新连接') }
            : { command: 'icg.bench.connect', title: t('Connect', '连接') };
        conn.contextValue = 'connection';
        items.push(conn);

        const chips = b.chips || [];
        const sel = b.state && b.state.chip;
        if (b.noChip) {
            // Connected, but no usable programmable chip (an empty world, or
            // hosts without a chip inserted): a normal state, not an error.
            const none = new vscode.TreeItem(
                t('no programmable chip in this world', '当前世界没有可编程芯片'),
                vscode.TreeItemCollapsibleState.None
            );
            none.iconPath = new vscode.ThemeIcon('info');
            none.tooltip = t(
                'Place an IC10 chip, or load a save that has one, then refresh.',
                '放一个 IC10 芯片，或载入带芯片的存档，然后刷新。'
            );
            items.push(none);
        } else if (chips.length) {
            // Group hosts: with code, with a chip but no code, and no chip at
            // all. Each sorted by name descending. The last two are collapsed by
            // default (there are usually many of them).
            const withCode = chips.filter((c) => c.programmable !== false && c.lines > 0);
            const noCode = chips.filter((c) => c.programmable !== false && !c.lines);
            const noChip = chips.filter((c) => c.programmable === false);
            const groups = [
                { label: t('Chip · code', '有芯片 · 有代码'), list: withCode, open: true, icon: 'circuit-board' },
                { label: t('Chip · no code', '有芯片 · 无代码'), list: noCode, open: false, icon: 'warning' },
                { label: t('No chip', '无芯片'), list: noChip, open: false, icon: 'circle-slash' },
            ];
            for (const g of groups) {
                if (!g.list.length) continue;
                g.list.sort(chipByNameDesc);
                const item = new vscode.TreeItem(
                    `${g.label} (${g.list.length})`,
                    g.open ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.Collapsed
                );
                item._kind = 'chipGroup';
                item._chips = g.list;
                item.contextValue = 'chipGroup';
                item.iconPath = new vscode.ThemeIcon(g.icon);
                items.push(item);
            }
        } else if (sel) {
            items.push(chipNode(sel, true));
        }
        return items;
    }

    chipGroupItems(el) {
        const sel = this.bench.state && this.bench.state.chip;
        return (el._chips || []).map((chip) => chipNode(chip, sameChip(chip, sel)));
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

// chipByNameDesc sorts chips by name (falling back to prefab) descending.
function chipByNameDesc(a, b) {
    const an = String(a.name || a.prefab || '');
    const bn = String(b.name || b.prefab || '');
    return bn.localeCompare(an, 'zh');
}

// isNoChip reports whether an error means the selected holder has no chip (or
// the world has no programmable chip), which the UI treats as a normal state.
function isNoChip(err) {
    const msg = (err && (err.message || err.code)) || '';
    return /no-chip|no chip|not a ProgrammableChip/i.test(msg);
}

// posPick builds a QuickPick item for a host found by position (locate --at).
function posPick(hit, prefix, tag) {
    const chip = hit.chip || hit;
    const name = chip.name || chip.prefab || `chip#${chip.index}`;
    let extra = '';
    if (chip.programmable === false) extra = ` · ${t('no chip', '无芯片')}`;
    else if (!chip.lines) extra = ` · ${t('no code', '无代码')}`;
    else extra = ` · ${chip.lines} ${t('lines', '行')}`;
    return {
        label: `${prefix}  ${name}`,
        description: [tag, chip.prefab].filter(Boolean).join(' · ') + extra,
        chip,
    };
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
    item.contextValue = 'chip';
    item.iconPath = new vscode.ThemeIcon(
        'circuit-board',
        selected ? new vscode.ThemeColor('charts.blue') : undefined
    );
    const tags = [];
    if (chip.programmable === false) tags.push(t('no chip', '无芯片'));
    else if (!chip.lines) tags.push(t('no code', '无代码'));
    if (chip.powered === false) tags.push(t('no power', '未通电'));
    if (chip.lines) tags.push(`${chip.lines} ${t('lines', '行')}`);
    item.description = tags.join(' · ');
    let tip = `**${name}**\n\n`;
    if (chip.prefab) tip += `\`${chip.prefab}\`\n\n`;
    if (chip.chipPrefab) tip += t('chip: ', '芯片：') + `\`${chip.chipPrefab}\`\n\n`;
    if (chip.programmable !== false && !chip.lines) {
        tip += t('no program on this chip', '这块 host 的芯片没有程序') + '\n\n';
    }
    if (chip.powered === false) tip += t('not powered', '未通电') + '\n\n';
    if (chip.pos) {
        const { x, y, z, yaw } = chip.pos;
        const yawPart = yaw ? ` · yaw ${Math.round(yaw)}°` : '';
        tip += t('at ', '位置：') + `(${x.toFixed(1)}, ${y.toFixed(1)}, ${z.toFixed(1)})${yawPart}\n\n`;
    }
    if (chip.fp) tip += `fp \`${chip.fp}\`\n\n`;
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
    try {
        if (el && el._kind === 'device') {
            const out = [];
            for (const k of Object.keys(el._logic || {})) {
                const item = new vscode.TreeItem(k, vscode.TreeItemCollapsibleState.None);
                const label = this.bench.prefabLabel(k, el._logic[k]);
                item.description = label ? `${el._logic[k]} · ${label}` : String(el._logic[k]);
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
    } catch (err) {
        return [];
    }
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
        this.sel = undefined; // chip selector pinned for state/set
        this.nearOnly = false; // show only hosts within ±4 of the player
        this.playerPos = undefined;
        this.watching = false;
        this.tree = undefined;
        this.panel = undefined;
        this.report = undefined;
        this.status = undefined;
        this.prefabs = undefined; // hash -> {name,title}, from the language server
        this.prefabsLoading = false;
        this.programMap = undefined; // {uri, map: IC10 line -> .icg line} of the last push
        this.runDecor = undefined;
        this.portsPanel = undefined;
        this.writesPanel = undefined;
        this.comparePanel = undefined;
        this.compareTimer = undefined;
        this.compareLive = false;
        this.writesBaseline = undefined; // key -> value captured with "Set baseline"
        this.lastWrites = [];
        this.store = undefined; // context.workspaceState
        this.restoreWatch = false;
        this.programData = undefined; // out.data from the last push (range/access)
    }

    // chipSel builds the protocol chip selector for the given chip.
    chipSel(chip) {
        if (!chip) return undefined;
        return chip.id ? { id: chip.id } : { index: chip.index };
    }

    // ensurePrefabs loads the prefab hash -> name table from the language
    // server once, then refreshes the tree and panel so hash-valued logic
    // (PrefabHash/NameHash/OccupantHash) shows the prefab it refers to.
    ensurePrefabs() {
        if (this.prefabs || this.prefabsLoading) return;
        this.prefabsLoading = true;
        this.client
            .request('ic10/prefabs', {})
            .then((r) => {
                this.prefabs = (r && r.prefabs) || {};
            })
            .catch(() => {
                // Older ic10c without ic10/prefabs: stop retrying (labeling off).
                this.prefabs = {};
            })
            .then(() => {
                this.prefabsLoading = false;
                if (this.tree) this.tree.refresh();
                this.renderPanel();
            });
    }

    // prefabLabel resolves a device logic value to a prefab name when the logic
    // key names a prefab hash. Returns '' when unknown or not a hash value.
    prefabLabel(logic, value) {
        if (!this.prefabs || !HASH_LOGIC.test(logic)) return '';
        const v = Number(value);
        if (!isFinite(v)) return '';
        const e = this.prefabs[(v >>> 0).toString()];
        return (e && e.name) || '';
    }

    // updateRunLine highlights the .icg line the chip is executing, using the
    // line map recorded on the last upload (instruction-level best effort).
    updateRunLine() {
        if (!this.runDecor) return;
        const editors = vscode.window.visibleTextEditors || [];
        for (const ed of editors) ed.setDecorations(this.runDecor, []);
        if (!this.cfg().highlightLine || !this.programMap || !this.state) return;
        const src = this.programMap.map && this.programMap.map[this.state.line];
        if (!src) return;
        const ed = editors.find((e) => e.document.uri.toString() === this.programMap.uri);
        if (!ed) return;
        const line = Math.min(Math.max(0, src - 1), Math.max(0, ed.document.lineCount - 1));
        ed.setDecorations(this.runDecor, [new vscode.Range(line, 0, line, 0)]);
    }

    cfg() {
        const c = vscode.workspace.getConfiguration('icg');
        return {
            host: c.get('bench.host') || '127.0.0.1',
            port: c.get('bench.port') || 7800,
            autoConnect: c.get('bench.autoConnect') !== false,
            refreshInterval: c.get('bench.refreshInterval') || 0,
            watchOnOpen: c.get('bench.watchOnOpen') === true,
            runTicks: c.get('bench.runTicks') || 10,
            highlightLine: c.get('bench.highlightLine') !== false,
        };
    }

    register(context) {
        this.status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 90);
        this.status.command = 'icg.bench.openPanel';
        context.subscriptions.push(this.status);
        this.setStatus(false);

        this.runDecor = vscode.window.createTextEditorDecorationType({
            isWholeLine: true,
            backgroundColor: new vscode.ThemeColor('editor.rangeHighlightBackground'),
        });
        context.subscriptions.push(this.runDecor);

        // Restore the last selected chip and the live-update toggle, so a reload
        // comes back to the same chip.
        this.store = context.workspaceState;
        if (this.store) {
            const sel = this.store.get('icg.bench.sel');
            if (sel && typeof sel === 'object') this.sel = sel;
            this.restoreWatch = this.store.get('icg.bench.watching') === true;
        }

        this.tree = new BenchTree(this);
        context.subscriptions.push(
            vscode.window.registerTreeDataProvider('icg.bench', this.tree)
        );

        const cmd = (name, fn) => context.subscriptions.push(vscode.commands.registerCommand(name, fn));
        cmd('icg.bench.connect', () => this.connect(false));
        cmd('icg.bench.disconnect', () => this.disconnect());
        cmd('icg.bench.connectionMenu', () => this.connectionMenu());
        cmd('icg.bench.push', () => this.push());
        cmd('icg.bench.pull', () => this.pull());
        cmd('icg.bench.refresh', () => this.refresh(true));
        cmd('icg.bench.watch', () => this.toggleWatch());
        cmd('icg.bench.pause', () => this.togglePause());
        cmd('icg.bench.step', () => this.runTicks(1));
        cmd('icg.bench.runTicks', () => this.runTicks(this.cfg().runTicks));
        cmd('icg.bench.reset', () => this.reset());
        cmd('icg.bench.ports', () => this.showPorts());
        cmd('icg.bench.writes', () => this.showWrites(false));
        cmd('icg.bench.compare', () => this.showCompare());
        cmd('icg.bench.exportStack', () => this.exportStack());
        cmd('icg.bench.loadSave', () => this.loadSave());
        cmd('icg.bench.runScenario', () => this.runScenario());
        cmd('icg.bench.openPanel', () => this.openPanel());
        cmd('icg.bench.selectChip', (chip) => this.selectChip(chip));
        cmd('icg.bench.track', (arg) => this.trackChip(arg));
        cmd('icg.bench.trackPlayer', () => this.trackPlayer());
        cmd('icg.bench.findAt', () => this.findByPos());
        cmd('icg.bench.filterNear', () => this.setNear(true));
        cmd('icg.bench.clearNear', () => this.setNear(false));
        vscode.commands.executeCommand('setContext', 'icg.bench.nearOnly', this.nearOnly);
        cmd('icg.bench.setDevice', (arg) => this.setDevice(arg));
        cmd('icg.bench.pulseDevice', (arg) => this.pulseDevice(arg));

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
        if (this.portsPanel) this.portsPanel.dispose();
        if (this.writesPanel) this.writesPanel.dispose();
        this.stopCompare();
        if (this.comparePanel) this.comparePanel.dispose();
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
            if (this.restoreWatch) {
                this.restoreWatch = false;
                if (!this.watching) this.toggleWatch();
            }
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
        this.sel = undefined;
        this.setStatus(false);
        if (this.tree) this.tree.refresh();
        this.renderPanel();
        this.updateRunLine();
    }

    onClose() {
        this.conn = undefined;
        this.watching = false;
        this.sel = undefined;
        this.setStatus(false);
        if (this.tree) this.tree.refresh();
        this.renderPanel();
        this.updateRunLine();
    }

    // connectionMenu is the connected tree node's action: reconnect,
    // disconnect, open the state panel, or refresh.
    async connectionMenu() {
        if (!this.conn) return this.connect(false);
        const reconnect = t('Reconnect', '重新连接');
        const disconnect = t('Disconnect', '断开连接');
        const panel = t('Open Chip State panel', '打开芯片状态面板');
        const refresh = t('Refresh', '刷新');
        const pick = await vscode.window.showQuickPick([reconnect, disconnect, panel, refresh], {
            placeHolder: t(`Connected to ${this.cfg().host}:${this.cfg().port}`, `已连接 ${this.cfg().host}:${this.cfg().port}`),
        });
        if (pick === reconnect) return this.reconnect();
        if (pick === disconnect) return this.disconnect();
        if (pick === panel) return this.openPanel();
        if (pick === refresh) return this.refresh(true);
    }

    // reconnect drops the socket and connects again, keeping the pinned chip and
    // the live-update toggle.
    async reconnect() {
        const sel = this.sel;
        const watching = this.watching;
        this.disconnect();
        this.sel = sel;
        const c = await this.connect(false);
        if (c && watching && !this.watching) this.toggleWatch();
        return c;
    }

    setStatus(connected) {
        if (!this.status) return;
        const chip = this.state && this.state.chip;
        let name = chip ? chip.name || chip.prefab || `chip#${chip.index}` : '';
        if (!name && this.noChip) {
            name = t('no chip', '无芯片');
        }
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
            this.noChip = false;
            const list = await c.call('chip.list', {}).catch(() => ({ chips: [] }));
            const all = list.chips || [];
            // Drop a stale pinned selection against the full list (the game
            // restarted and the id may now belong to a different holder).
            if (this.sel && !all.some((ch) => this.sameSel(ch, this.sel))) {
                this.sel = undefined;
            }
            if (all.length === 0) {
                // No programmable chip in this world: a normal state, not an
                // error. Clear the panel, show it in the tree, and say so once.
                this.chips = all;
                this.noChip = true;
                this.sel = undefined;
                this.state = undefined;
                if (this.tree) this.tree.refresh();
                this.renderPanel();
                this.setStatus(true);
                if (interactive) {
                    vscode.window.showInformationMessage(t(
                        'IC10: connected, but this world has no programmable chip.',
                        'IC10: 已连接，但当前世界没有可编程芯片。'
                    ));
                }
                return;
            }
            this.chips = this.nearOnly ? await this.filterNear(c, all) : all;
            let st;
            try {
                st = await this.fetchState(c);
            } catch (err) {
                if (this.sel) {
                    this.sel = undefined;
                    st = await this.fetchState(c).catch((err2) => {
                        if (isNoChip(err2)) {
                            this.noChip = true;
                            return undefined;
                        }
                        throw err2;
                    });
                } else if (isNoChip(err)) {
                    // The default holder has no chip inserted.
                    this.noChip = true;
                    st = undefined;
                } else {
                    throw err;
                }
            }
            this.state = st;
            if (st && !this.sel && st.chip) {
                this.sel = this.chipSel(st.chip);
                this.persistSel();
            }
            if (this.tree) this.tree.refresh();
            this.renderPanel();
            this.setStatus(true);
            this.ensurePrefabs();
            this.updateRunLine();
        } catch (err) {
            this.client.output.appendLine(`IC10 bench refresh failed: ${err.message}`);
            if (interactive) vscode.window.showErrorMessage(t('IC10: refresh failed. See the "IC10 Go" output.', 'IC10: 刷新失败，详见 "IC10 Go" 输出面板。'));
        }
    }

    sameSel(chip, sel) {
        return sel && sel.id !== undefined ? chip.id === sel.id : chip.index === sel.index;
    }

    // persistSel remembers the pinned chip across window reloads.
    persistSel() {
        if (this.store) this.store.update('icg.bench.sel', this.sel || null);
    }

    fetchState(c) {
        const args = { include: ['registers', 'stack', 'devices', 'program', 'errors'], all: true };
        if (this.sel) args.chip = this.sel;
        return c.call('state', args);
    }

    async selectChip(chip) {
        if (!chip) return;
        const c = await this.connect(false);
        if (!c) return;
        try {
            this.sel = this.chipSel(chip);
            this.persistSel();
            await c.call('chip.select', { chip: this.sel });
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
            // A source with several `chip` blocks returns one entry per block;
            // ask which block to upload (the CLI equivalent is `push --as NAME`).
            let code = out.code;
            let lineMap = out.lineMap;
            let data = out.data;
            let chipLabel = '';
            if (Array.isArray(out.chips) && out.chips.length > 1) {
                const item = await vscode.window.showQuickPick(
                    out.chips.map((c, i) => ({
                        label: c.name || `chip ${i + 1}`,
                        description: t(`${(c.lines || []).length} lines`, `${(c.lines || []).length} 行`),
                        index: i,
                    })),
                    { placeHolder: t('Choose a chip block to upload', '选择要上传的芯片块') }
                );
                if (!item) return;
                const c = out.chips[item.index];
                code = c.code;
                lineMap = c.lineMap;
                data = { needed: !!(c.loader || (c.loaders && c.loaders.length)), loader: c.loader, loaders: c.loaders, setup: c.setup };
                chipLabel = c.name || '';
            }
            const loaders = (data && (data.loaders || (data.loader ? [data.loader] : []))) || [];
            if (Array.isArray(lineMap) && lineMap.length) {
                this.programMap = { uri: doc.uri.toString(), map: lineMap };
            } else {
                this.programMap = undefined;
            }
            this.programData = data || undefined;
            try {
                const r = await conn.call('push', { code, loaders });
                const lines = r.lines || 0;
                const tail = chipLabel ? ` [${chipLabel}]` : '';
                const loaderNote = loaders.length ? ` (+${loaders.length} loader)` : '';
                vscode.window.setStatusBarMessage(
                    t(`IC10: uploaded ${lines} lines${tail}${loaderNote}`,
                        `IC10: 已上传 ${lines} 行${tail}${loaders.length ? `（+${loaders.length} 段 loader）` : ''}`),
                    4000
                );
                await this.refresh(false);
            } catch (err) {
                this.client.output.appendLine(`IC10 bench push failed: ${err.message}`);
                vscode.window.showErrorMessage(t('IC10: upload failed. See the "IC10 Go" output.', 'IC10: 上传失败，详见 "IC10 Go" 输出面板。'));
            }
        });
    }

    // pull downloads the selected chip's current IC10 source into a new editor,
    // mirroring push. The user can then decompile it back to .icg or save it.
    async pull() {
        const conn = await this.connect(false);
        if (!conn) return;
        let r;
        try {
            const args = {};
            if (this.sel) args.chip = this.sel;
            r = await conn.call('program', args);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench pull failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: download failed. See the "IC10 Go" output.', 'IC10: 下载失败，详见 "IC10 Go" 输出面板。'));
            return;
        }
        const code = r.code || '';
        if (!code.trim()) {
            vscode.window.showWarningMessage(t('IC10: the chip has no program to download.', 'IC10: 芯片没有可下载的程序。'));
            return;
        }
        const chip = this.state && this.state.chip;
        const name = chip ? chip.name || chip.prefab || `chip#${chip.index}` : t('chip', '芯片');
        const lines = r.lines || code.replace(/\r/g, '').split('\n').length;
        const doc = await vscode.workspace.openTextDocument({ content: code, language: 'ic10' });
        await vscode.window.showTextDocument(doc, { viewColumn: vscode.ViewColumn.Beside, preview: false });
        this.client.output.appendLine(`=== download: ${name} (${lines} lines) ===\n${code}`);
        const decompile = t('Decompile to .icg', '反编译为 .icg');
        const save = t('Save as…', '另存为…');
        const pick = await vscode.window.showInformationMessage(
            t(`IC10: downloaded ${lines} lines from ${name}.`, `IC10: 已从 ${name} 下载 ${lines} 行。`),
            decompile,
            save
        );
        if (pick === decompile) await vscode.commands.executeCommand('icg.decompile');
        else if (pick === save) await vscode.commands.executeCommand('workbench.action.files.saveAs');
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

    // runTicks advances the selected chip by n game ticks (128 instructions
    // each). Stepping is deterministic while the world is paused, so pause it
    // first when needed.
    async runTicks(n) {
        const conn = await this.connect(false);
        if (!conn) return;
        if (!(this.state && this.state.paused)) {
            try {
                await conn.call('pause', { on: true });
                vscode.window.setStatusBarMessage(t('IC10: paused for stepping', 'IC10: 已暂停以便单步'), 3000);
            } catch (err) {
                // Not fatal: run still advances the chip.
            }
        }
        const args = { ticks: Math.max(1, Math.floor(n) || 1) };
        if (this.sel) args.chip = this.sel;
        try {
            const r = await conn.call('run', args);
            if (r && r.error) {
                this.client.output.appendLine(`IC10 bench run: ${JSON.stringify(r.error)}`);
                vscode.window.showErrorMessage(t('IC10: chip error while running. See the "IC10 Go" output.', 'IC10: 运行中芯片报错，详见 "IC10 Go" 输出面板。'));
            }
            await this.refresh(false);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench run failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: run failed. See the "IC10 Go" output.', 'IC10: 运行失败，详见 "IC10 Go" 输出面板。'));
        }
    }

    // reset clears the chip's registers, PC and error state (the stack is kept).
    async reset() {
        const conn = await this.connect(false);
        if (!conn) return;
        const args = {};
        if (this.sel) args.chip = this.sel;
        try {
            await conn.call('reset', args);
            await this.refresh(false);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench reset failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: reset failed. See the "IC10 Go" output.', 'IC10: 重置失败，详见 "IC10 Go" 输出面板。'));
        }
    }

    // showPorts opens a panel describing how the selected chip's ports (d0..d5
    // over two cable networks) are wired, from the mod's `ports` diagnostic.
    async showPorts() {
        const c = await this.connect(false);
        if (!c) return;
        const args = {};
        if (this.sel) args.chip = this.sel;
        let r;
        try {
            r = await c.call('ports', args);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench ports failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: ports failed. See the "IC10 Go" output.', 'IC10: 读取端口失败，详见 "IC10 Go" 输出面板。'));
            return;
        }
        if (this.portsPanel) {
            this.portsPanel.webview.html = this.portsHtml(r);
        } else {
            this.portsPanel = vscode.window.createWebviewPanel(
                'icg.benchPorts',
                t('IC10 Port Wiring', 'IC10 端口接线'),
                vscode.ViewColumn.Beside,
                { enableScripts: true }
            );
            this.portsPanel.webview.html = this.portsHtml(r);
            this.portsPanel.onDidDispose(() => {
                this.portsPanel = undefined;
            });
            this.portsPanel.webview.onDidReceiveMessage((m) => {
                if (m && m.type === 'refresh') this.showPorts();
            });
        }
    }

    portsHtml(r) {
        const nonce = String(Date.now()) + Math.random().toString(36).slice(2);
        const chip = r.chip || {};
        const chipName = chip.name || chip.prefab || (chip.index !== undefined ? `chip#${chip.index}` : t('chip', '芯片'));
        const rows = [];
        for (const e of r.lookups || []) {
            if (!e) continue;
            const logic = e.logic && typeof e.logic === 'object'
                ? Object.keys(e.logic).map((k) => `${k}=${e.logic[k]}`).join(' ')
                : '';
            rows.push({
                port: 'd' + e.deviceIndex,
                net: String(e.networkIndex),
                dev: e.name || e.type || '',
                prefab: e.prefab || '',
                logic,
            });
        }
        if (!rows.length) {
            (r.devices || []).forEach((d, i) => {
                if (!d) return;
                rows.push({ port: 'd' + i, net: '0', dev: d.name || d.type || '', prefab: d.prefab || '', logic: '' });
            });
        }
        const body = rows.length
            ? rows
                  .map(
                      (x) =>
                          `<tr><td class="port">${escapeHtml(x.port)}</td><td>${escapeHtml(x.net)}</td><td>${escapeHtml(x.dev)}</td><td>${escapeHtml(x.prefab)}</td><td class="muted">${escapeHtml(x.logic)}</td></tr>`
                  )
                  .join('')
            : `<tr><td colspan="5" class="muted">${t('no ports bound', '没有绑定端口')}</td></tr>`;
        const bindings = (r.bindings || []).filter((b) => b != null).join(' · ');
        const raw = escapeHtml(JSON.stringify(r, null, 1));
        return `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
  body { font-family: var(--vscode-font-family); color: var(--vscode-foreground); padding: 14px 16px; }
  h1 { font-size: 14px; margin: 0 0 2px; }
  .sub { color: var(--vscode-descriptionForeground); margin-bottom: 12px; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: 4px 9px; border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.25)); vertical-align: top; }
  th { color: var(--vscode-descriptionForeground); font-weight: 500; }
  td.port { font-family: var(--vscode-editor-font-family, monospace); color: var(--vscode-charts-blue, #3794ff); }
  .muted { color: var(--vscode-descriptionForeground); }
  button { font-family: inherit; color: var(--vscode-button-secondaryForeground, var(--vscode-foreground)); background: var(--vscode-button-secondaryBackground, transparent); border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35)); border-radius: 5px; padding: 3px 10px; cursor: pointer; }
  pre { background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.08)); padding: 10px; border-radius: 6px; overflow: auto; max-height: 320px; }
  details { margin-top: 14px; }
</style></head>
<body>
<h1>${escapeHtml(chipName)}</h1>
<div class="sub">${bindings ? escapeHtml(t('bindings: ', '绑定：') + bindings) : ''} <button id="refresh">${t('Refresh', '刷新')}</button></div>
<table><thead><tr><th>${t('Port', '端口')}</th><th>${t('Net', '网络')}</th><th>${t('Device', '设备')}</th><th>${t('Prefab', '预制体')}</th><th>${t('Logic', '逻辑')}</th></tr></thead><tbody>${body}</tbody></table>
<details><summary>${t('Raw report', '原始报告')}</summary><pre>${raw}</pre></details>
<script nonce="${nonce}">document.getElementById('refresh').addEventListener('click', () => acquireVsCodeApi().postMessage({ type: 'refresh' }));</script>
</body></html>`;
    }

    // showWrites opens a panel with the device-logic writes the program has
    // performed since the last clear (the mod's Harmony write trace). Clear,
    // then Step/Run, then Refresh to see exactly what one tick writes.
    async showWrites(clear) {
        const c = await this.connect(false);
        if (!c) return;
        const args = clear ? { clear: true } : {};
        if (this.sel) args.chip = this.sel;
        let r;
        try {
            r = await c.call('writes', args);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench writes failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: writes failed. See the "IC10 Go" output.', 'IC10: 读取写序列失败，详见 "IC10 Go" 输出面板。'));
            return;
        }
        this.lastWrites = r.writes || [];
        const html = this.writesHtml(r, this.baselineMap());
        if (this.writesPanel) {
            this.writesPanel.webview.html = html;
        } else {
            this.writesPanel = vscode.window.createWebviewPanel(
                'icg.benchWrites',
                t('IC10 Device Writes', 'IC10 设备写序列'),
                vscode.ViewColumn.Beside,
                { enableScripts: true }
            );
            this.writesPanel.webview.html = html;
            this.writesPanel.onDidDispose(() => {
                this.writesPanel = undefined;
                this.writesBaseline = undefined;
            });
            this.writesPanel.webview.onDidReceiveMessage((m) => {
                if (!m) return;
                if (m.type === 'refresh') this.showWrites(false);
                else if (m.type === 'clear') this.showWrites(true);
                else if (m.type === 'baseline') {
                    this.writesBaseline = this.lastWrites || [];
                    this.showWrites(false);
                }
            });
        }
    }

    // writesKey identifies one writable target across snapshots. It mirrors the
    // device label the panel renders (name, else "id:<ReferenceId>").
    writesKey(w) {
        const slot = w.slot !== undefined && w.slot !== null ? w.slot : -1;
        const device = w.device || (w.id ? `id:${w.id}` : '');
        return `${device}|${w.logic || ''}|${slot}`;
    }

    // baselineMap reduces the baseline snapshot to the last value per target.
    baselineMap() {
        if (!this.writesBaseline) return undefined;
        const m = {};
        for (const w of this.writesBaseline) m[this.writesKey(w)] = w.value;
        return m;
    }

    writesHtml(r, baseline) {
        const nonce = String(Date.now()) + Math.random().toString(36).slice(2);
        const all = r.writes || [];
        const max = 500;
        const shown = all.length > max ? all.slice(all.length - max) : all;
        let changedCount = 0;
        const rows = shown
            .map((w) => {
                const device = w.device || (w.id ? `id:${w.id}` : '');
                const logic = w.slot !== undefined && w.slot !== null && w.slot >= 0 ? `${w.logic}[${w.slot}]` : w.logic || '';
                let diff = '';
                let cls = '';
                if (baseline) {
                    const key = `${device}|${w.logic || ''}|${w.slot !== undefined && w.slot !== null ? w.slot : -1}`;
                    const prev = baseline[key];
                    if (prev === undefined) diff = t('new', '新增');
                    else if (prev !== w.value) diff = t('was ', '原 ') + prev;
                    if (diff) {
                        changedCount++;
                        cls = ' class="changed"';
                    }
                }
                return `<tr${cls}><td class="muted">${escapeHtml(String(w.seq))}</td><td>${escapeHtml(device)}</td><td class="port">${escapeHtml(String(logic))}</td><td class="num">${escapeHtml(String(w.value))}</td><td class="muted">${escapeHtml(diff)}</td></tr>`;
            })
            .join('');
        const body = rows || `<tr><td colspan="5" class="muted">${t('no writes recorded', '没有记录到写入')}</td></tr>`;
        const note = all.length > shown.length ? `<p class="muted">${escapeHtml(t(`showing the last ${shown.length} of ${all.length}`, `显示最近 ${shown.length} / 共 ${all.length} 条`))}</p>` : '';
        const diffNote = baseline
            ? `<p class="muted">${escapeHtml(t(`${changedCount} changed vs baseline`, `与基线相比 ${changedCount} 处不同`))}</p>`
            : '';
        return `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
  body { font-family: var(--vscode-font-family); color: var(--vscode-foreground); padding: 14px 16px; }
  h1 { font-size: 14px; margin: 0 0 8px; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: 3px 9px; border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.25)); }
  th { color: var(--vscode-descriptionForeground); font-weight: 500; position: sticky; top: 0; background: var(--vscode-editor-background); }
  td.num { text-align: right; font-family: var(--vscode-editor-font-family, monospace); font-variant-numeric: tabular-nums; }
  td.port { font-family: var(--vscode-editor-font-family, monospace); color: var(--vscode-charts-blue, #3794ff); }
  tr.changed { background: var(--vscode-diffEditor-insertedLineBackground, rgba(63,185,80,.12)); }
  .muted { color: var(--vscode-descriptionForeground); }
  button { font-family: inherit; color: var(--vscode-button-secondaryForeground, var(--vscode-foreground)); background: var(--vscode-button-secondaryBackground, transparent); border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35)); border-radius: 5px; padding: 3px 10px; cursor: pointer; margin-right: 6px; }
  label { color: var(--vscode-descriptionForeground); margin-left: 6px; }
  body.only-changed tr:not(.changed) { display: none; }
</style></head>
<body>
<h1>${t('Device writes since the last clear', '自上次清空以来的设备写入')}</h1>
<div><button id="refresh">${t('Refresh', '刷新')}</button><button id="clear">${t('Clear', '清空')}</button><button id="baseline">${t('Set baseline', '设为基线')}</button><label><input type="checkbox" id="onlyChanged" /> ${t('changed only', '只看变化')}</label></div>
${note}${diffNote}
<table><thead><tr><th>${t('Seq', '序号')}</th><th>${t('Device', '设备')}</th><th>${t('Logic', '逻辑')}</th><th>${t('Value', '值')}</th><th>${t('Diff', '差异')}</th></tr></thead><tbody>${body}</tbody></table>
<script nonce="${nonce}">
  const vscode = acquireVsCodeApi();
  document.getElementById('refresh').addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));
  document.getElementById('clear').addEventListener('click', () => vscode.postMessage({ type: 'clear' }));
  document.getElementById('baseline').addEventListener('click', () => vscode.postMessage({ type: 'baseline' }));
  document.getElementById('onlyChanged').addEventListener('change', (e) => {
    document.body.className = e.target.checked ? 'only-changed' : '';
  });
</script>
</body></html>`;
    }

    // showCompare opens a panel that shows every chip's state side by side, so
    // a multi-chip program (chips talking over a bus) can be watched at once.
    showCompare() {
        if (this.comparePanel) {
            this.comparePanel.reveal(vscode.ViewColumn.Beside);
        } else {
            this.comparePanel = vscode.window.createWebviewPanel(
                'icg.benchCompare',
                t('IC10 Chips', 'IC10 多芯片对比'),
                vscode.ViewColumn.Beside,
                { enableScripts: true, retainContextWhenHidden: true }
            );
            this.comparePanel.webview.html = this.compareHtml();
            this.comparePanel.onDidDispose(() => {
                this.comparePanel = undefined;
                this.stopCompare();
            });
            this.comparePanel.webview.onDidReceiveMessage((m) => {
                if (!m) return;
                if (m.type === 'refresh') this.refreshCompare();
                else if (m.type === 'live') this.toggleCompareLive();
                else if (m.type === 'select' && m.index !== undefined) this.selectChip({ index: m.index });
            });
        }
        this.refreshCompare();
    }

    async refreshCompare() {
        if (!this.comparePanel) return;
        const c = await this.connect(true);
        if (!c) {
            this.comparePanel.webview.postMessage({ type: 'compare', connected: false, chips: [] });
            return;
        }
        let chips = [];
        try {
            chips = ((await c.call('chip.list', {})).chips) || [];
        } catch (err) {
            this.client.output.appendLine(`IC10 compare chip.list failed: ${err.message}`);
        }
        const cols = [];
        for (const chip of chips) {
            if (chip.programmable === false) {
                cols.push({ chip });
                continue;
            }
            const sel = chip.id ? { id: chip.id } : { index: chip.index };
            try {
                const st = await c.call('state', {
                    chip: sel,
                    include: ['registers', 'stack', 'devices', 'program', 'errors'],
                });
                cols.push({ chip, state: st });
            } catch (err) {
                cols.push({ chip, error: err.message });
            }
        }
        this.comparePanel.webview.postMessage({ type: 'compare', connected: true, chips: cols });
    }

    toggleCompareLive() {
        this.compareLive = !this.compareLive;
        this.stopCompare();
        if (this.compareLive) {
            const interval = this.cfg().refreshInterval > 0 ? this.cfg().refreshInterval : 500;
            this.compareTimer = setInterval(() => this.refreshCompare(), interval);
        }
        if (this.comparePanel) {
            this.comparePanel.webview.postMessage({ type: 'live', on: this.compareLive });
        }
    }

    stopCompare() {
        if (this.compareTimer) clearInterval(this.compareTimer);
        this.compareTimer = undefined;
    }

    compareHtml() {
        const nonce = String(Date.now()) + Math.random().toString(36).slice(2);
        return `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
  :root { color-scheme: light dark; }
  body { font-family: var(--vscode-font-family); font-size: var(--vscode-font-size); color: var(--vscode-foreground); margin: 0; }
  header { position: sticky; top: 0; z-index: 2; display: flex; align-items: center; gap: 8px; padding: 10px 16px; background: var(--vscode-sideBar-background, var(--vscode-editor-background)); border-bottom: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35)); }
  header .title { font-weight: 600; }
  button { font-family: inherit; font-size: inherit; color: var(--vscode-button-secondaryForeground, var(--vscode-foreground)); background: var(--vscode-button-secondaryBackground, transparent); border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.35)); border-radius: 5px; padding: 3px 10px; cursor: pointer; }
  button.active { color: var(--vscode-button-foreground); background: var(--vscode-button-background); border-color: var(--vscode-button-background); }
  #cols { display: flex; gap: 12px; align-items: flex-start; overflow-x: auto; padding: 14px 16px 32px; }
  .col { flex: 0 0 auto; width: 268px; border: 1px solid var(--vscode-editorWidget-border, rgba(128,128,128,.25)); border-radius: 8px; padding: 10px 12px; }
  .chead { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 4px; }
  .cname { font-weight: 600; }
  .line { color: var(--vscode-descriptionForeground); margin-bottom: 6px; font-variant-numeric: tabular-nums; }
  .err { color: var(--vscode-errorForeground, #f14c4c); margin-bottom: 6px; }
  table.regs { width: 100%; border-collapse: collapse; }
  table.regs td { padding: 1px 6px; }
  table.regs tr.special td { color: var(--vscode-charts-orange, #d18616); }
  td.num { text-align: right; font-family: var(--vscode-editor-font-family, monospace); font-variant-numeric: tabular-nums; }
  .port { color: var(--vscode-charts-blue, #3794ff); font-family: var(--vscode-editor-font-family, monospace); }
  .dev { margin-top: 6px; }
  .logic { display: flex; justify-content: space-between; gap: 8px; font-family: var(--vscode-editor-font-family, monospace); font-size: 12px; color: var(--vscode-descriptionForeground); }
  .muted { color: var(--vscode-descriptionForeground); }
  .pill { font-size: 10px; padding: 1px 7px; border-radius: 999px; background: var(--vscode-badge-background); color: var(--vscode-badge-foreground); }
  details { margin-top: 6px; }
  details > summary { cursor: pointer; color: var(--vscode-descriptionForeground); }
  table.regs tr.changed td { background: var(--vscode-diffEditor-insertedLineBackground, rgba(63,185,80,.18)); }
  .spbar { height: 5px; border-radius: 3px; background: var(--vscode-editorWidget-border, rgba(128,128,128,.3)); overflow: hidden; margin: 6px 0 2px; }
  .spbar > div { height: 100%; background: var(--vscode-charts-orange, #d18616); }
  .chead[data-index] { cursor: pointer; }
  header label { color: var(--vscode-descriptionForeground); }
</style></head>
<body>
<header>
  <span class="title">${t('Chips', '芯片')}</span>
  <button id="refresh">${t('Refresh', '刷新')}</button>
  <button id="live">${t('Live', '实时')}</button>
  <label><input type="checkbox" id="diffOnly" /> ${t('diff only', '只看差异')}</label>
</header>
<div id="cols"><p class="muted" style="padding:12px">${t('not connected', '未连接')}</p></div>
<script nonce="${nonce}">
  const vscode = acquireVsCodeApi();
  let live = false;
  let diffOnly = false;
  let prev = {};
  let lastMsg = null;
  document.getElementById('refresh').addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));
  document.getElementById('live').addEventListener('click', () => vscode.postMessage({ type: 'live' }));
  document.getElementById('diffOnly').addEventListener('change', (e) => {
    diffOnly = e.target.checked;
    if (lastMsg) render(lastMsg);
  });
  document.getElementById('cols').addEventListener('click', (e) => {
    const h = e.target && e.target.closest ? e.target.closest('.chead[data-index]') : null;
    if (h) vscode.postMessage({ type: 'select', index: Number(h.getAttribute('data-index')) });
  });
  window.addEventListener('message', (e) => {
    const m = e.data;
    if (!m) return;
    if (m.type === 'live') { live = !!m.on; document.getElementById('live').className = live ? 'active' : ''; return; }
    if (m.type === 'compare') render(m);
  });
  function esc(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }
  function num(v) {
    if (v === null || v === undefined) return '–';
    if (Number.isInteger(v)) return String(v);
    return String(Number(v.toPrecision(10)));
  }
  const ORDER = ['r0','r1','r2','r3','r4','r5','r6','r7','r8','r9','r10','r11','r12','r13','r14','r15','ra','sp'];
  function chipKey(chip) { return chip.id !== undefined && chip.id !== null ? 'id:' + chip.id : 'i:' + chip.index; }
  function col(c, vary, fresh) {
    const chip = c.chip || {};
    const key = chipKey(chip);
    const name = chip.name || chip.prefab || ('chip#' + chip.index);
    let h = '<div class="col"><div class="chead" data-index="' + esc(String(chip.index)) + '" title="${t('select this chip', '选中该芯片')}"><span class="cname">' + esc(name) + '</span><span class="pill">' +
      (chip.programmable === false ? '${t('no chip', '无芯片')}' : '#' + esc(String(chip.index))) + '</span></div>';
    if (c.error) return h + '<div class="err">' + esc(c.error) + '</div></div>';
    const st = c.state;
    if (!st) return h + '</div>';
    h += '<div class="line">' + (st.line !== undefined ? 'line ' + st.line : '') +
      (st.program ? ' / ' + st.program.lines : '') + (st.paused ? ' · paused' : '') + '</div>';
    if (st.errors && (st.errors.code || st.errors.compilation)) {
      h += '<div class="err">' + esc(st.errors.code || 'error') + ' line ' + st.errors.line + '</div>';
    }
    if (st.registers) {
      fresh[key] = {};
      h += '<table class="regs">';
      for (const k of ORDER) {
        if (!(k in st.registers)) continue;
        const v = st.registers[k];
        fresh[key][k] = v;
        if (diffOnly && !vary[k]) continue;
        const was = prev[key] ? prev[key][k] : undefined;
        const changed = was !== undefined && was !== v;
        h += '<tr class="' + (k === 'sp' || k === 'ra' ? 'special ' : '') + (changed ? 'changed' : '') + '"><td>' + k + '</td><td class="num">' + num(v) + '</td></tr>';
      }
      h += '</table>';
    }
    if (st.stack) {
      const sp = st.stack.sp || 0;
      const size = st.stack.size || 512;
      const pct = Math.max(0, Math.min(100, (sp / size) * 100));
      h += '<div class="spbar" title="sp ' + sp + ' / ' + size + '"><div style="width:' + pct.toFixed(1) + '%"></div></div>';
    }
    if (st.devices && st.devices.length) {
      const devs = st.devices.filter((d) => d.present !== false);
      h += '<details><summary>' + devs.length + ' devices</summary>';
      for (const d of devs) {
        h += '<div class="dev"><span class="port">' + esc(d.port) + '</span> <span class="muted">' + esc(d.binding || '') + '</span></div>';
        for (const k of Object.keys(d.logic || {}).sort()) {
          h += '<div class="logic"><span>' + esc(k) + '</span><span>' + num(d.logic[k]) + '</span></div>';
        }
      }
      h += '</details>';
    }
    return h + '</div>';
  }
  function render(m) {
    lastMsg = m;
    const wrap = document.getElementById('cols');
    if (!m.connected) { wrap.innerHTML = '<p class="muted" style="padding:12px">${t('not connected', '未连接')}</p>'; return; }
    const cols = m.chips || [];
    if (!cols.length) { wrap.innerHTML = '<p class="muted" style="padding:12px">${t('no chips', '没有芯片')}</p>'; prev = {}; return; }
    const vary = {};
    for (const k of ORDER) {
      let first;
      let seen = false;
      let differs = false;
      for (const c of cols) {
        const r = c.state && c.state.registers;
        if (!r || !(k in r)) continue;
        if (!seen) { first = r[k]; seen = true; }
        else if (r[k] !== first) differs = true;
      }
      vary[k] = differs;
    }
    const fresh = {};
    wrap.innerHTML = cols.map((c) => col(c, vary, fresh)).join('');
    prev = fresh;
  }
</script>
</body></html>`;
    }

    // buildStackLoader renders stack slots as a pasteable loader, mirroring the
    // compiler's data loader (`put db <slot> <value>`, or `poke` for a chip
    // whose runtime reads its own stack). It returns chunks that each fit the
    // editor line limit, to be run in order.
    buildStackLoader(pairs, access, maxLines) {
        const write = access === 'stack' ? 'poke' : 'put db';
        const limit = maxLines > 0 ? maxLines : 128;
        const lines = pairs.map(([slot, v]) => `${write} ${slot} ${fmtStackValue(v)}`);
        const chunks = [];
        for (let i = 0; i < lines.length; i += limit) {
            chunks.push(lines.slice(i, i + limit).join('\n') + '\n');
        }
        return chunks;
    }

    // exportStack reads the selected chip's stack and writes a loader that
    // reinstalls the chosen slots, so initialized stack data can be moved to
    // another chip or saved. Each <=128-line chunk opens in its own editor and
    // the first is copied to the clipboard.
    async exportStack() {
        const c = await this.connect(false);
        if (!c) return;
        const args = { include: ['stack'] };
        if (this.sel) args.chip = this.sel;
        let st;
        try {
            st = await c.call('state', args);
        } catch (err) {
            this.client.output.appendLine(`IC10 bench stack failed: ${err.message}`);
            vscode.window.showErrorMessage(t('IC10: stack export failed. See the "IC10 Go" output.', 'IC10: 导出栈数据失败，详见 "IC10 Go" 输出面板。'));
            return;
        }
        const stack = st && st.stack;
        if (!stack || !stack.values) {
            vscode.window.showWarningMessage(t('IC10: the chip has no stack to export.', 'IC10: 芯片没有可导出的栈数据。'));
            return;
        }
        const vals = stack.values;
        const keys = Object.keys(vals).map(Number).sort((a, b) => a - b);
        const sp = stack.sp || 0;
        const data = this.programData && this.programData.needed ? this.programData : undefined;
        const options = [];
        if (data && typeof data.start === 'number' && data.start >= 0) {
            options.push({ label: t('Data segment', '数据段'), description: `[${data.start}..${data.end}]`, kind: 'range' });
        }
        options.push({ label: t('Used stack (0..sp)', '已用栈（0..sp）'), description: `0..${sp}`, kind: 'used' });
        options.push({ label: t('Non-zero slots', '非零槽位'), kind: 'nonzero' });
        options.push({ label: t('All slots with a value', '全部有值的槽位'), kind: 'all' });
        const pick = await vscode.window.showQuickPick(options, {
            placeHolder: t('Export which stack range?', '导出哪一段栈数据？'),
        });
        if (!pick) return;
        let slots;
        switch (pick.kind) {
            case 'range':
                slots = [];
                for (let i = data.start; i <= data.end; i++) slots.push(i);
                break;
            case 'used':
                slots = keys.filter((k) => k <= sp);
                break;
            case 'nonzero':
                slots = keys.filter((k) => Number(vals[String(k)]) !== 0);
                break;
            default:
                slots = keys;
        }
        if (!slots.length) {
            vscode.window.showWarningMessage(t('IC10: nothing to export in that range.', 'IC10: 该范围内没有可导出的数据。'));
            return;
        }
        const pairs = slots.map((k) => [k, vals[String(k)] === undefined ? 0 : vals[String(k)]]);
        const access = (this.programData && this.programData.access) || 'get';
        const maxLines = this.client.config().maxLines > 0 ? this.client.config().maxLines : 128;
        const chunks = this.buildStackLoader(pairs, access, maxLines);
        for (const chunk of chunks) {
            const doc = await vscode.workspace.openTextDocument({ content: chunk, language: 'ic10' });
            await vscode.window.showTextDocument(doc, { viewColumn: vscode.ViewColumn.Beside, preview: false });
        }
        await vscode.env.clipboard.writeText(chunks[0]);
        const write = access === 'stack' ? 'poke' : 'put db';
        this.client.output.appendLine(
            `=== stack export: ${pairs.length} slots, ${chunks.length} chunk(s), ${write} ===\n${chunks.join('\n')}`
        );
        vscode.window.setStatusBarMessage(
            t(
                `IC10: exported ${pairs.length} stack slots as ${chunks.length} loader chunk(s) (${write}); first chunk copied`,
                `IC10: 已导出 ${pairs.length} 个栈槽为 ${chunks.length} 段 loader（${write}），首段已复制`
            ),
            6000
        );
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
        if (this.store) this.store.update('icg.bench.watching', this.watching);
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
        this.renderPanel();
    }

    onEvent(ev) {
        if (ev.event === 'state' && ev.state) {
            this.state = ev.state;
            if (this.tree) this.tree.refresh();
            this.renderPanel();
            this.setStatus(true);
            this.updateRunLine();
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
            const setArgs = { writes: [{ port: arg.port, logic: arg.logic, value }] };
            if (this.sel) setArgs.chip = this.sel;
            await c.call('set', setArgs);
            await this.refresh(false);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: set failed: ', 'IC10: 设置失败：') + err.message);
        }
    }

    // pulseDevice writes 0 then 1 so edge-triggered logic (e.g. a jetpack's
    // Activate) fires.
    async pulseDevice(arg) {
        if (!arg || !arg.port || !arg.logic) return;
        const c = await this.connect(false);
        if (!c) return;
        try {
            const req = { writes: [{ port: arg.port, logic: arg.logic, value: 1 }], pulse: true };
            if (this.sel) req.chip = this.sel;
            await c.call('set', req);
            await this.refresh(false);
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: pulse failed: ', 'IC10: 脉冲失败：') + err.message);
        }
    }

    // trackChip points the in-game HUD at a host so the player can follow the
    // compass arrow to the chip (mod >= 0.3.0; F8 toggles the overlay).
    async trackChip(arg) {
        const chip = (arg && arg._chip) ? arg._chip : arg;
        if (!chip || chip.index === undefined) return;
        const name = chip.name || chip.prefab || `chip#${chip.index}`;
        const res = await this.client.execCli(['testbench', 'hud', '--chip', String(chip.index)]);
        if (res.code !== 0) {
            const msg = (res.stderr || res.stdout || '').trim();
            vscode.window.showErrorMessage(t('IC10: track failed: ', 'IC10: 追踪失败：') + msg);
            return;
        }
        vscode.window.setStatusBarMessage(
            t(`IC10: tracking ${name} in game (F8 toggles the HUD)`,
              `IC10: 游戏中追踪 ${name}（F8 开关 HUD）`), 6000);
    }

    // setNear turns the "only hosts within ±4 of me" filter on/off and refreshes.
    async setNear(on) {
        this.nearOnly = !!on;
        vscode.commands.executeCommand('setContext', 'icg.bench.nearOnly', this.nearOnly);
        if (this.nearOnly) {
            vscode.window.setStatusBarMessage(
                t('IC10: showing hosts within ±4 of you', 'IC10: 只显示你 ±4 格内的 host'), 4000);
        }
        await this.refresh(false);
    }

    // filterNear keeps hosts within ±4 blocks of the player on every axis.
    async filterNear(c, all) {
        try {
            const h = await c.call('hud', {});
            this.playerPos = h && h.player ? h.player : undefined;
        } catch (err) {
            // keep the last known position
        }
        const p = this.playerPos;
        if (!p) return all;
        const R = 4;
        return all.filter((ch) =>
            ch.pos &&
            Math.abs(ch.pos.x - p.x) <= R &&
            Math.abs(ch.pos.y - p.y) <= R &&
            Math.abs(ch.pos.z - p.z) <= R);
    }

    // findByPos asks for a coordinate and an optional approximate range, then
    // lists the hosts there: exact (decimals ignored) first, then within range.
    // Picking one selects it, like clicking it in the tree.
    async findByPos() {
        const input = await vscode.window.showInputBox({
            prompt: t('Position "X Y Z" (exact match ignores decimals)', '坐标「X Y Z」（精确匹配忽略小数）'),
            placeHolder: '669 192 -627',
            validateInput: (v) =>
                v.trim().split(/[\s,]+/).filter(Boolean).length === 3 ? undefined : t('need three numbers', '需要三个数字'),
        });
        if (input === undefined) return;
        const xyz = input.trim().split(/[\s,]+/).filter(Boolean).slice(0, 3);
        const range = await vscode.window.showInputBox({
            prompt: t('Approximate range in blocks (blank = exact only)', '大致位置半径（格）；留空 = 只看精确'),
            placeHolder: '12',
        });
        if (range === undefined) return;

        const args = ['testbench', 'locate', '--at', ...xyz, '--json'];
        if (range.trim()) args.push('--range', range.trim());
        const res = await this.client.execCli(args);
        let out;
        try {
            out = JSON.parse(res.stdout);
        } catch (err) {
            this.client.output.appendLine(`=== locate --at failed ===\n${res.stderr || res.stdout}`);
            this.client.output.show(true);
            vscode.window.showErrorMessage(
                t('IC10: find by position failed. See the "IC10 Go" output.', 'IC10: 按坐标查找失败，详见 "IC10 Go" 输出面板。')
            );
            return;
        }
        const exact = out.exact || [];
        const near = out.near || [];
        if (!exact.length && !near.length) {
            vscode.window.showInformationMessage(
                t(`No host near ${xyz.join(' ')}`, `没找到 ${xyz.join(' ')} 附近的 host`)
            );
            return;
        }
        const items = exact
            .map((h) => posPick(h, `$(pinned) ${t('exact', '精确')}`, t('exact', '精确')))
            .concat(near.map((h) => posPick(h, `$(location) ${h.dist.toFixed(1)} m`, `${h.dist.toFixed(1)} m`)));
        const pick = await vscode.window.showQuickPick(items, {
            title: t(`Hosts at ${xyz.join(' ')}`, `${xyz.join(' ')} 附近的 host`),
            placeHolder: t('Select to make it the current chip', '选择后切换为当前芯片'),
        });
        if (pick && pick.chip) this.selectChip(pick.chip);
    }

    // trackPlayer lists the players and points the in-game HUD at the chosen
    // one (the mod reads its position live, so it keeps up as they move).
    async trackPlayer() {
        const c = await this.connect(true);
        if (!c) {
            vscode.window.showWarningMessage(t('IC10: not connected to the game.', 'IC10: 未连接到游戏。'));
            return;
        }
        let players = [];
        try {
            players = ((await c.call('players', {})).players) || [];
        } catch (err) {
            vscode.window.showErrorMessage(t('IC10: players failed: ', 'IC10: 获取玩家失败：') + err.message);
            return;
        }
        if (!players.length) {
            vscode.window.showInformationMessage(t('IC10: no players.', 'IC10: 没有玩家。'));
            return;
        }
        const items = players.map((p) => {
            const online = p.online !== false; // absent => assume online
            const tags = [];
            if (p.self) tags.push(t('you', '你'));
            else tags.push(online ? t('online', '在线') : t('offline', '离线'));
            if (p.dist != null) tags.push(`${p.dist.toFixed(1)} m`);
            return {
                label: (p.self ? '$(account) ' : online ? '$(person) ' : '$(circle-slash) ') + (p.name || t('(unnamed)', '(无名)')),
                description: tags.join(' · '),
                player: p,
                online,
            };
        });
        const pick = await vscode.window.showQuickPick(items, {
            title: t('Track a player', '追踪玩家'),
            placeHolder: t('The in-game HUD will point at them', '游戏内 HUD 会指向他'),
        });
        if (!pick) return;
        if (!pick.online) {
            vscode.window.showWarningMessage(t(`IC10: ${pick.player.name} is offline.`, `IC10: ${pick.player.name} 离线，无法追踪。`));
            return;
        }
        const res = await this.client.execCli(['testbench', 'hud', '--player', pick.player.name]);
        if (res.code !== 0) {
            vscode.window.showErrorMessage(
                t('IC10: track failed: ', 'IC10: 追踪失败：') + (res.stderr || res.stdout || '').trim()
            );
            return;
        }
        vscode.window.setStatusBarMessage(t(`IC10: tracking ${pick.player.name}`, `IC10: 正在追踪 ${pick.player.name}`), 5000);
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
        this.ensurePrefabs();
        this.renderPanel();
    }

    onPanelMessage(m) {
        if (!m) return;
        if (m.type === 'refresh') this.refresh(true);
        else if (m.type === 'watch') this.toggleWatch();
        else if (m.type === 'pause') this.togglePause();
        else if (m.type === 'step') this.runTicks(1);
        else if (m.type === 'run') this.runTicks(this.cfg().runTicks);
        else if (m.type === 'reset') this.reset();
        else if (m.type === 'copyHash' && m.name) this.copyHash(m.name);
        else if (m.type === 'setDevice' && m.port && m.logic) this.setDevice({ port: m.port, logic: m.logic, value: m.value });
        else if (m.type === 'pulse' && m.port && m.logic) this.pulseDevice({ port: m.port, logic: m.logic });
    }

    // copyHash puts the source form of a prefab hash on the clipboard.
    copyHash(name) {
        const text = `hash("${name}")`;
        vscode.env.clipboard.writeText(text).then(
            () => vscode.window.setStatusBarMessage(t(`IC10: copied ${text}`, `IC10: 已复制 ${text}`), 3000),
            () => vscode.window.showErrorMessage(t('IC10: could not copy to the clipboard.', 'IC10: 复制到剪贴板失败。'))
        );
    }

    renderPanel() {
        if (!this.panel) return;
        this.panel.webview.postMessage({
            type: 'state',
            state: this.state,
            connected: !!this.conn,
            watching: this.watching,
            prefabs: this.prefabs || {},
            runTicks: this.cfg().runTicks,
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
  .hashlabel { cursor: pointer; text-decoration: underline dotted; }
  #filter {
    font-family: inherit; font-size: inherit;
    color: var(--vscode-input-foreground); background: var(--vscode-input-background);
    border: 1px solid var(--vscode-input-border, rgba(128,128,128,.35));
    border-radius: 5px; padding: 2px 8px; width: 140px;
  }
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
  tr.logicrow { cursor: pointer; }
  tr.logicrow:hover td { background: var(--vscode-list-hoverBackground, rgba(128,128,128,.12)); }
  td.act { width: 1%; white-space: nowrap; }
  button.pulse { padding: 0 7px; line-height: 1.4; }
  details.slots {
    margin-top: 6px; padding: 3px 9px; border-radius: 6px;
    border: 1px dashed var(--vscode-editorWidget-border, rgba(128,128,128,.35));
  }
  details.slots > summary, details.slot > summary {
    text-transform: none; letter-spacing: 0; font-size: var(--vscode-font-size); font-weight: 500; margin: 0;
  }
  details.slot { margin: 2px 0 2px 8px; }
  details.slot table { margin-top: 2px; }
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
  <input id="filter" type="search" placeholder="filter…" />
  <span style="flex:1"></span>
  <span class="line" id="line"></span>
  <button id="pause">Pause</button>
  <button id="step">Step</button>
  <button id="run">Run</button>
  <button id="reset">Reset</button>
  <button id="watch">Watch</button>
  <button id="refresh">Refresh</button>
</header>
<main id="main"><p class="empty">Not connected.</p></main>
<script nonce="${nonce}">
  const vscode = acquireVsCodeApi();
  let prev = {};
  let prefabs = {};
  let filter = '';
  let lastMsg = null;
  document.getElementById('filter').addEventListener('input', (e) => {
    filter = (e.target.value || '').trim().toLowerCase();
    if (lastMsg) render(lastMsg);
  });
  document.getElementById('refresh').addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));
  document.getElementById('watch').addEventListener('click', () => vscode.postMessage({ type: 'watch' }));
  document.getElementById('pause').addEventListener('click', () => vscode.postMessage({ type: 'pause' }));
  document.getElementById('step').addEventListener('click', () => vscode.postMessage({ type: 'step' }));
  document.getElementById('run').addEventListener('click', () => vscode.postMessage({ type: 'run' }));
  document.getElementById('reset').addEventListener('click', () => vscode.postMessage({ type: 'reset' }));
  const openMap = {};
  document.getElementById('main').addEventListener('click', (e) => {
    const el = e.target;
    const h = el && el.closest ? el.closest('.hashlabel') : null;
    if (h) {
      vscode.postMessage({ type: 'copyHash', name: h.getAttribute('data-name') });
      return;
    }
    const p = el && el.closest ? el.closest('.pulse') : null;
    if (p) {
      const row = p.closest('tr.logicrow');
      if (row) vscode.postMessage({ type: 'pulse', port: row.getAttribute('data-port'), logic: row.getAttribute('data-logic') });
      return;
    }
    const r = el && el.closest ? el.closest('tr.logicrow') : null;
    if (r) {
      vscode.postMessage({ type: 'setDevice', port: r.getAttribute('data-port'), logic: r.getAttribute('data-logic'), value: r.getAttribute('data-value') });
      return;
    }
    const s = el && el.closest ? el.closest('summary') : null;
    if (!s) return;
    const det = s.parentElement;
    if (!det || det.tagName !== 'DETAILS') return;
    const key = det.getAttribute('data-key');
    if (key) setTimeout(() => { openMap[key] = det.open; }, 0);
  });
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
  function esc(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }
  const HASH_LOGIC = /^(PrefabHash|NameHash|OccupantHash)$/;
  function hashTag(logic, v) {
    if (!HASH_LOGIC.test(logic)) return '';
    const n = Number(v);
    if (!isFinite(n)) return '';
    const e = prefabs[(n >>> 0).toString()];
    if (!e) return '';
    const tip = e.title ? ' title="' + esc(e.title) + '"' : '';
    return ' <span class="muted hashlabel" data-name="' + esc(e.name) + '"' + tip + '>' + esc(e.name) + '</span>';
  }
  function devMatches(d, f) {
    const hay = [d.port, d.binding, d.name, d.prefab].filter(Boolean).join(' ').toLowerCase();
    if (hay.indexOf(f) >= 0) return true;
    for (const k of Object.keys(d.logic || {})) if (k.toLowerCase().indexOf(f) >= 0) return true;
    return false;
  }
  function cell(k, v, special) {
    const changed = prev[k] !== undefined && prev[k] !== v;
    return '<div class="cell' + (special ? ' special' : '') + (changed ? ' changed' : '') + '">' +
      '<span class="k">' + k + '</span><span class="v">' + num(v) + '</span></div>';
  }
  function render(m) {
    const st = m.state;
    lastMsg = m;
    if (m.prefabs) prefabs = m.prefabs;
    document.getElementById('dot').className = 'dot' + (m.connected ? ' on' : '');
    document.getElementById('watch').className = m.watching ? 'active' : '';
    const pauseBtn = document.getElementById('pause');
    if (pauseBtn) {
      const paused = !!(st && st.paused);
      pauseBtn.textContent = paused ? 'Resume' : 'Pause';
      pauseBtn.className = paused ? 'active' : '';
    }
    const runBtn = document.getElementById('run');
    if (runBtn && m.runTicks) runBtn.textContent = 'Run ' + m.runTicks;
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
      html += '<section><details' + (openMap['stack'] ? ' open' : '') + ' data-key="stack"><summary>Stack <span class="pill">sp ' + sp + ' / ' + (st.stack.size || keys.length || '?') + ' · ' + keys.length + ' slots</span></summary><div class="grid">';
      for (const i of keys) {
        const v = vals[String(i)];
        next['[' + i + ']'] = v;
        html += cell('[' + i + ']', v, i === sp);
      }
      html += '</div></details></section>';
    }
    if (st.devices && st.devices.length) {
      const devs = filter ? st.devices.filter((d) => devMatches(d, filter)) : st.devices;
      html += '<section><h2>Devices</h2>';
      if (!devs.length) html += '<p class="muted">no match</p>';
      for (const d of devs) {
        const keys = Object.keys(d.logic || {}).sort();
        const binding = d.binding ? '<span class="k">' + d.binding + '</span>' : '';
        const present = d.present !== false;
        if (!present || !keys.length) {
          html += '<div class="dev empty"><span class="port">' + d.port + '</span> ' + binding +
            ' <span class="muted">empty</span></div>';
          continue;
        }
        const desc = d.name || d.prefab || '';
        const slotCount = d.slots && d.slots.length ? d.slots.length : 0;
        html += '<details class="dev"' + (openMap['dev:' + d.port] ? ' open' : '') + ' data-key="dev:' + d.port + '"><summary><span class="port">' + d.port + '</span> ' + binding +
          ' <span class="muted">' + desc + '</span> <span class="pill">' + keys.length + ' logic' + (slotCount ? ' · ' + slotCount + ' slots' : '') + '</span></summary><table>';
        for (const k of keys) {
          next[d.port + '.' + k] = d.logic[k];
          html += '<tr class="logicrow" data-port="' + d.port + '" data-logic="' + k + '" data-value="' + num(d.logic[k]) +
            '" title="${t('click to set', '点击修改')}"><td>' + k + '</td><td class="num">' + num(d.logic[k]) + hashTag(k, d.logic[k]) +
            '</td><td class="act"><button class="pulse" title="${t('pulse 0 then 1', '脉冲：写 0 再写 1')}">⚡</button></td></tr>';
        }
        html += '</table>';
        if (d.probe && Object.keys(d.probe).length) {
          html += '<table class="probe">';
          for (const pk of Object.keys(d.probe)) {
            html += '<tr><td class="muted">' + pk + '</td><td class="num">' + d.probe[pk] + '</td></tr>';
          }
          html += '</table>';
        }
        if (slotCount) {
          const skey = 'slots:' + d.port;
          html += '<details class="slots"' + (openMap[skey] ? ' open' : '') + ' data-key="' + skey +
            '"><summary>Slots <span class="pill">' + slotCount + '</span></summary>';
          for (const s of d.slots) {
            const lk = Object.keys(s.logic || {}).sort();
            const bits = [];
            if (s.logic && s.logic.Occupied !== undefined) bits.push('Occupied=' + num(s.logic.Occupied));
            if (s.logic && s.logic.Quantity !== undefined) bits.push('Quantity=' + num(s.logic.Quantity));
            if (s.logic && s.logic.Class !== undefined) bits.push('Class=' + num(s.logic.Class));
            const key = 'slot:' + d.port + ':' + s.index;
            html += '<details class="slot"' + (openMap[key] ? ' open' : '') + ' data-key="' + key +
              '"><summary>slot ' + s.index + (bits.length ? ' <span class="muted">' + bits.join('  ') + '</span>' : '') + '</summary><table>';
            for (const k of lk) {
              html += '<tr><td>' + k + '</td><td class="num">' + num(s.logic[k]) + hashTag(k, s.logic[k]) + '</td></tr>';
            }
            html += '</table></details>';
          }
          html += '</details>';
        }
        html += '</details>';
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
